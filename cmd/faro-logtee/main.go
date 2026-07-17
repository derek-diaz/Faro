package main

import (
	"bufio"
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const storageRetryInterval = 30 * time.Second

func main() {
	path := flag.String("path", "/var/log/coredns/query.log", "log file path")
	maxBytes := flag.Int64("max-bytes", 10*1024*1024, "maximum bytes per log file")
	backups := flag.Int("backups", 2, "number of rotated log files to retain")
	flag.Parse()

	if err := run(os.Stdin, os.Stdout, *path, *maxBytes, *backups); err != nil {
		log.Fatal(err)
	}
}

func run(input io.Reader, output io.Writer, path string, maxBytes int64, backups int) error {
	writer, err := newRotatingWriter(path, maxBytes, backups)
	if err != nil {
		return err
	}
	defer writer.Close()
	destination := io.MultiWriter(output, writer)
	reader := bufio.NewReader(input)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if _, err := destination.Write(line); err != nil {
				return err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

type rotatingWriter struct {
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
	retryAt  time.Time
}

func newRotatingWriter(path string, maxBytes int64, backups int) (*rotatingWriter, error) {
	if path == "" {
		return nil, errors.New("log path is required")
	}
	if maxBytes < 1 {
		return nil, errors.New("max-bytes must be positive")
	}
	if backups < 1 {
		return nil, errors.New("backups must be at least 1")
	}
	w := &rotatingWriter{path: path, maxBytes: maxBytes, backups: backups}
	if err := w.reclaimLegacyLogs(); err != nil {
		return nil, err
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) Write(data []byte) (int, error) {
	if w.file == nil {
		if time.Now().Before(w.retryAt) {
			return len(data), nil
		}
		if err := w.emergencyReset(); err != nil {
			if errors.Is(err, syscall.ENOSPC) {
				w.pauseForFullStorage(err)
				return len(data), nil
			}
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(data)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			if errors.Is(err, syscall.ENOSPC) {
				w.pauseForFullStorage(err)
				return len(data), nil
			}
			return 0, err
		}
	}
	n, err := w.file.Write(data)
	w.size += int64(n)
	if errors.Is(err, syscall.ENOSPC) {
		if resetErr := w.emergencyReset(); resetErr != nil {
			if errors.Is(resetErr, syscall.ENOSPC) {
				w.pauseForFullStorage(resetErr)
				return len(data), nil
			}
			return n, errors.Join(err, resetErr)
		}
		retry, retryErr := w.file.Write(data)
		w.size = int64(retry)
		if retryErr != nil {
			if errors.Is(retryErr, syscall.ENOSPC) {
				w.pauseForFullStorage(retryErr)
				return len(data), nil
			}
			return retry, retryErr
		}
		log.Printf("query log storage filled; older raw log buffers were discarded so logging could continue")
		return retry, nil
	}
	return n, err
}

func (w *rotatingWriter) Close() error {
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = stat.Size()
	return nil
}

func (w *rotatingWriter) rotate() error {
	if err := w.Close(); err != nil {
		return err
	}
	_ = os.Remove(w.rotatedPath(w.backups))
	for index := w.backups - 1; index >= 1; index-- {
		if err := os.Rename(w.rotatedPath(index), w.rotatedPath(index+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(w.path, w.rotatedPath(1)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := w.open(); err != nil {
		if !errors.Is(err, syscall.ENOSPC) {
			return err
		}
		// A full filesystem may allow the rename above but reject creation of
		// the new active file. Sacrifice the just-rotated buffer and retry.
		if removeErr := os.Remove(w.rotatedPath(1)); removeErr != nil && !os.IsNotExist(removeErr) {
			return errors.Join(err, removeErr)
		}
		log.Printf("query log storage filled during rotation; discarded the oldest raw buffer")
		return w.open()
	}
	return nil
}

func (w *rotatingWriter) rotatedPath(index int) string {
	return w.path + "." + strconv.Itoa(index)
}

// reclaimLegacyLogs makes upgrades from the old unbounded tee safe even when
// the existing log has consumed all free space. Truncating the active file and
// removing oversized backups do not require allocating another full copy.
func (w *rotatingWriter) reclaimLegacyLogs() error {
	matches, err := filepath.Glob(w.path + ".*")
	if err != nil {
		return err
	}
	for _, match := range matches {
		index, parseErr := strconv.Atoi(strings.TrimPrefix(match, w.path+"."))
		if parseErr != nil || index < 1 {
			continue
		}
		stat, statErr := os.Stat(match)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return statErr
		}
		if index > w.backups || stat.Size() > w.maxBytes {
			if err := os.Remove(match); err != nil && !os.IsNotExist(err) {
				return err
			}
			log.Printf("removed oversized legacy query-log backup %s", filepath.Base(match))
		}
	}
	stat, err := os.Stat(w.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if stat.Size() <= w.maxBytes {
		return nil
	}
	if err := os.Truncate(w.path, 0); err != nil {
		return err
	}
	log.Printf("truncated oversized legacy query log from %d bytes", stat.Size())
	return nil
}

func (w *rotatingWriter) emergencyReset() error {
	if err := w.Close(); err != nil {
		return err
	}
	for index := 1; index <= w.backups; index++ {
		if err := os.Remove(w.rotatedPath(index)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Truncate(w.path, 0); err != nil && !os.IsNotExist(err) {
		return err
	}
	return w.open()
}

func (w *rotatingWriter) pauseForFullStorage(cause error) {
	_ = w.Close()
	w.retryAt = time.Now().Add(storageRetryInterval)
	log.Printf("raw query-log persistence paused for %s because storage is full: %v", storageRetryInterval, cause)
}
