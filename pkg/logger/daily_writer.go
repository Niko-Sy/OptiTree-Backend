// Package logger provides a daily-rotating log file writer.
// Each day a new file named <dir>/app-YYYY-MM-DD.log is created.
// The writer is safe for concurrent use.
package logger

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DailyFileWriter is an io.Writer that rotates to a new file each calendar day.
type DailyFileWriter struct {
	mu      sync.Mutex
	dir     string
	file    *os.File
	curDate string
}

// NewDailyFileWriter creates a DailyFileWriter that stores logs under dir.
// The directory is created on first write if it does not exist.
func NewDailyFileWriter(dir string) *DailyFileWriter {
	return &DailyFileWriter{dir: dir}
}

// Write implements io.Writer. It transparently rotates to a new file when the
// calendar date changes.
func (w *DailyFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if w.curDate != today {
		if w.file != nil {
			_ = w.file.Close()
		}
		if err := os.MkdirAll(w.dir, 0o755); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(
			filepath.Join(w.dir, "app-"+today+".log"),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY,
			0o644,
		)
		if err != nil {
			return 0, err
		}
		w.file = f
		w.curDate = today
	}
	return w.file.Write(p)
}

// Close releases the underlying file handle.
func (w *DailyFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
