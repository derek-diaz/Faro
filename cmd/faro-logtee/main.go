package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
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

func run(input io.Reader, output io.Writer, path string, maxBytes int64, backups int) (runErr error) {
	writer, err := newRotatingWriter(path, maxBytes, backups)
	if err != nil {
		return err
	}
	defer func() {
		if err := writer.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close log writer: %w", err))
		}
	}()
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
	handled, err := w.prepareForWrite(len(data))
	if handled {
		return len(data), nil
	}
	if err != nil {
		return 0, err
	}
	return w.writeData(data)
}

// prepareForWrite opens the active file and rotates it when the next line
// would exceed the configured size. It returns handled=true when the line was
// intentionally dropped because storage is full or a retry is already paused.
func (w *rotatingWriter) prepareForWrite(dataLength int) (handled bool, err error) {
	if w.file == nil {
		if time.Now().Before(w.retryAt) {
			return true, nil
		}
		if err := w.emergencyReset(); err != nil {
			return w.handleStorageError(dataLength, err)
		}
	}
	if w.size > 0 && w.size+int64(dataLength) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return w.handleStorageError(dataLength, err)
		}
	}
	return false, nil
}

func (w *rotatingWriter) handleStorageError(dataLength int, err error) (bool, error) {
	if !errors.Is(err, syscall.ENOSPC) {
		return false, err
	}
	w.pauseForFullStorage(err)
	return true, nil
}

func (w *rotatingWriter) writeData(data []byte) (int, error) {
	n, err := w.file.Write(data)
	w.size += int64(n)
	if !errors.Is(err, syscall.ENOSPC) {
		return n, err
	}
	return w.retryAfterStorageFull(data, n, err)
}

func (w *rotatingWriter) retryAfterStorageFull(data []byte, initialWritten int, initialErr error) (int, error) {
	if resetErr := w.emergencyReset(); resetErr != nil {
		handled, storageErr := w.handleStorageError(len(data), resetErr)
		if handled {
			return len(data), nil
		}
		return initialWritten, errors.Join(initialErr, storageErr)
	}

	retry, retryErr := w.file.Write(data)
	w.size = int64(retry)
	if retryErr == nil {
		log.Printf("query log storage filled; older raw log buffers were discarded so logging could continue")
		return retry, nil
	}
	handled, storageErr := w.handleStorageError(len(data), retryErr)
	if handled {
		return len(data), nil
	}
	return retry, storageErr
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
	if err := w.reclaimLegacyBackups(); err != nil {
		return err
	}
	return w.truncateOversizedLegacyLog()
}

func (w *rotatingWriter) reclaimLegacyBackups() error {
	matches, err := filepath.Glob(w.path + ".*")
	if err != nil {
		return err
	}
	for _, match := range matches {
		if err := w.reclaimLegacyBackup(match); err != nil {
			return err
		}
	}
	return nil
}

func (w *rotatingWriter) reclaimLegacyBackup(path string) error {
	index, err := strconv.Atoi(strings.TrimPrefix(path, w.path+"."))
	if err != nil || index < 1 {
		return nil
	}
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if index <= w.backups && stat.Size() <= w.maxBytes {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	log.Printf("removed obsolete legacy query-log backup %s", filepath.Base(path))
	return nil
}

func (w *rotatingWriter) truncateOversizedLegacyLog() error {
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
	if err := w.Close(); err != nil {
		log.Printf("close query log after storage error: %v", err)
	}
	w.retryAt = time.Now().Add(storageRetryInterval)
	log.Printf("raw query-log persistence paused for %s because storage is full: %v", storageRetryInterval, cause)
}
