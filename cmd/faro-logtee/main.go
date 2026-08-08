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
	writer := &rotatingWriter{path: path, maxBytes: maxBytes, backups: backups}
	if err := writer.reclaimLegacyLogs(); err != nil {
		return nil, err
	}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (writer *rotatingWriter) Write(data []byte) (int, error) {
	handled, err := writer.prepareForWrite(len(data))
	if handled {
		return len(data), nil
	}
	if err != nil {
		return 0, err
	}
	return writer.writeData(data)
}

// prepareForWrite opens the active file and rotates it when the next line
// would exceed the configured size. It returns handled=true when the line was
// intentionally dropped because storage is full or a retry is already paused.
func (writer *rotatingWriter) prepareForWrite(dataLength int) (handled bool, err error) {
	if writer.file == nil {
		if time.Now().Before(writer.retryAt) {
			return true, nil
		}
		if err := writer.emergencyReset(); err != nil {
			return writer.handleStorageError(dataLength, err)
		}
	}
	if writer.size > 0 && writer.size+int64(dataLength) > writer.maxBytes {
		if err := writer.rotate(); err != nil {
			return writer.handleStorageError(dataLength, err)
		}
	}
	return false, nil
}

func (writer *rotatingWriter) handleStorageError(dataLength int, err error) (bool, error) {
	if !errors.Is(err, syscall.ENOSPC) {
		return false, err
	}
	writer.pauseForFullStorage(err)
	return true, nil
}

func (writer *rotatingWriter) writeData(data []byte) (int, error) {
	n, err := writer.file.Write(data)
	writer.size += int64(n)
	if !errors.Is(err, syscall.ENOSPC) {
		return n, err
	}
	return writer.retryAfterStorageFull(data, n, err)
}

func (writer *rotatingWriter) retryAfterStorageFull(data []byte, initialWritten int, initialErr error) (int, error) {
	if resetErr := writer.emergencyReset(); resetErr != nil {
		handled, storageErr := writer.handleStorageError(len(data), resetErr)
		if handled {
			return len(data), nil
		}
		return initialWritten, errors.Join(initialErr, storageErr)
	}

	retry, retryErr := writer.file.Write(data)
	writer.size = int64(retry)
	if retryErr == nil {
		log.Printf("query log storage filled; older raw log buffers were discarded so logging could continue")
		return retry, nil
	}
	handled, storageErr := writer.handleStorageError(len(data), retryErr)
	if handled {
		return len(data), nil
	}
	return retry, storageErr
}

func (writer *rotatingWriter) Close() error {
	if writer.file == nil {
		return nil
	}
	err := writer.file.Close()
	writer.file = nil
	return err
}

func (writer *rotatingWriter) open() error {
	file, err := os.OpenFile(writer.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	writer.file = file
	writer.size = stat.Size()
	return nil
}

func (writer *rotatingWriter) rotate() error {
	if err := writer.Close(); err != nil {
		return err
	}
	_ = os.Remove(writer.rotatedPath(writer.backups))
	for index := writer.backups - 1; index >= 1; index-- {
		if err := os.Rename(writer.rotatedPath(index), writer.rotatedPath(index+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(writer.path, writer.rotatedPath(1)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := writer.open(); err != nil {
		if !errors.Is(err, syscall.ENOSPC) {
			return err
		}
		// A full filesystem may allow the rename above but reject creation of
		// the new active file. Sacrifice the just-rotated buffer and retry.
		if removeErr := os.Remove(writer.rotatedPath(1)); removeErr != nil && !os.IsNotExist(removeErr) {
			return errors.Join(err, removeErr)
		}
		log.Printf("query log storage filled during rotation; discarded the oldest raw buffer")
		return writer.open()
	}
	return nil
}

func (writer *rotatingWriter) rotatedPath(index int) string {
	return writer.path + "." + strconv.Itoa(index)
}

// reclaimLegacyLogs makes upgrades from the old unbounded tee safe even when
// the existing log has consumed all free space. Truncating the active file and
// removing oversized backups do not require allocating another full copy.
func (writer *rotatingWriter) reclaimLegacyLogs() error {
	if err := writer.reclaimLegacyBackups(); err != nil {
		return err
	}
	return writer.truncateOversizedLegacyLog()
}

func (writer *rotatingWriter) reclaimLegacyBackups() error {
	matches, err := filepath.Glob(writer.path + ".*")
	if err != nil {
		return err
	}
	for _, match := range matches {
		if err := writer.reclaimLegacyBackup(match); err != nil {
			return err
		}
	}
	return nil
}

func (writer *rotatingWriter) reclaimLegacyBackup(path string) error {
	index, err := strconv.Atoi(strings.TrimPrefix(path, writer.path+"."))
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
	if index <= writer.backups && stat.Size() <= writer.maxBytes {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	log.Printf("removed obsolete legacy query-log backup %s", filepath.Base(path))
	return nil
}

func (writer *rotatingWriter) truncateOversizedLegacyLog() error {
	stat, err := os.Stat(writer.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if stat.Size() <= writer.maxBytes {
		return nil
	}
	if err := os.Truncate(writer.path, 0); err != nil {
		return err
	}
	log.Printf("truncated oversized legacy query log from %d bytes", stat.Size())
	return nil
}

func (writer *rotatingWriter) emergencyReset() error {
	if err := writer.Close(); err != nil {
		return err
	}
	for index := 1; index <= writer.backups; index++ {
		if err := os.Remove(writer.rotatedPath(index)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Truncate(writer.path, 0); err != nil && !os.IsNotExist(err) {
		return err
	}
	return writer.open()
}

func (writer *rotatingWriter) pauseForFullStorage(cause error) {
	if err := writer.Close(); err != nil {
		log.Printf("close query log after storage error: %v", err)
	}
	writer.retryAt = time.Now().Add(storageRetryInterval)
	log.Printf("raw query-log persistence paused for %s because storage is full: %v", storageRetryInterval, cause)
}
