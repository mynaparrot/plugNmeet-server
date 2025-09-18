package logging

import (
	"fmt"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// SourceFormatter is a custom formatter to control the caller output.
// It wraps a standard formatter (like TextFormatter or JSONFormatter).
type SourceFormatter struct {
	// Underlying is the formatter (e.g., &logrus.TextFormatter{}) that we will delegate to.
	Underlying logrus.Formatter
	// AddSpace indicates whether to add an extra newline for readability, typically for text format.
	AddSpace bool
}

// Format renders a single log entry.
func (f *SourceFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	// If the logger has caller reporting enabled, the entry will have a `Caller` field.
	if entry.HasCaller() {
		// We format the caller information exactly as we want it.
		// Here, we just want the base file name and the line number.
		fileName := filepath.Base(entry.Caller.File)
		sourceLocation := fmt.Sprintf("%s:%d", fileName, entry.Caller.Line)

		// Add our custom-formatted source as a new field.
		// This will appear as `x_file_source="health.go:216"` in the log output.
		entry.Data["x_file_source"] = sourceLocation

		// The default TextFormatter doesn't add a 'func' field, but if you were using
		// a different formatter, you could remove it like this for a cleaner output:
		// delete(entry.Data, "func")
	}

	// Now, let the underlying formatter do the rest of the work with our modified entry.
	formatted, err := f.Underlying.Format(entry)
	if err != nil {
		return nil, err
	}

	if f.AddSpace {
		return append(formatted, '\n'), nil
	}

	return formatted, nil
}
