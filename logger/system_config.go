package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// SystemConfig defines the configuration the main system or a subsystem with custom settings.
type SystemConfig struct {
	Output   io.Writer // Output(s) for log entries (stderr, files, …)
	MinLevel Level     // Minimum level to log. Below this are ignored.
	Format   int       // Controls what is shown in log entry
}

// Creates a new system config with default production values.
//   Logs info level and above to stderr.
func NewProductionSystemConfig() *SystemConfig {
	result := SystemConfig{
		Output:   os.Stderr,
		MinLevel: LevelInfo,
		Format:   IncludeDate | IncludeTime | IncludeFile | IncludeLevel,
	}
	return &result
}

// Creates a new system config with default development values.
//   Logs verbose level and above to stderr.
func NewDevelopmentSystemConfig() *SystemConfig {
	result := SystemConfig{
		Output:   os.Stderr,
		MinLevel: LevelVerbose,
		Format:   IncludeDate | IncludeTime | IncludeFile | IncludeLevel,
	}
	return &result
}

// Adds a file to the existing log outputs
func (config *SystemConfig) AddFile(filePath string) error {
	logFileName := filepath.FromSlash(filePath)
	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	config.Output = io.MultiWriter(config.Output, logFile)
	return nil
}

// Sets a file as the only log output
func (config *SystemConfig) SetFile(filePath string) error {
	logFileName := filepath.FromSlash(filePath)
	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	config.Output = logFile
	return nil
}

// Adds a writer to the existing log outputs
func (config *SystemConfig) AddWriter(writer io.Writer) {
	config.Output = io.MultiWriter(config.Output, writer)
}

// Sets a writer as the only log output
func (config *SystemConfig) SetWriter(writer io.Writer) {
	config.Output = writer
}

type LogEntry struct {
	Date         string `json:"date,omitempty"`
	Time         string `json:"time,omitempty"`
	MicroSeconds string `json:"ms,omitempty"`
	System       string `json:"system,omitempty"`
	File         string `json:"file,omitempty"`
	Level        string `json:"level,omitempty"`
	Trace        string `json:"trace,omitempty"`
	Message      string `json:"message,omitempty"`
}

// Logs a JSON entry based on the system config
func (config *SystemConfig) logJSON(system string, level Level, depth int, trace, format string,
	values ...interface{}) error {

	if config.MinLevel > level {
		return nil // Level is below minimum
	}

	// Create log entry
	now := time.Now()

	entry := LogEntry{}

	// Append Date
	if config.Format&IncludeDate != 0 {
		year, month, day := now.Date()
		entry.Date = fmt.Sprintf("%04d/%02d/%02d", year, month, day)
	}

	// Append Time
	if config.Format&IncludeTime != 0 {
		hour, min, sec := now.Clock()
		entry.Time = fmt.Sprintf("%02d:%02d:%02d", hour, min, sec)
	}

	// Append microseconds
	if config.Format&IncludeMicro != 0 {
		entry.MicroSeconds = fmt.Sprintf("%06d", now.Nanosecond()/1e3)
	}

	// Append System
	if config.Format&IncludeSystem != 0 {
		entry.System = system
	}

	// Append File
	if config.Format&IncludeFile != 0 {
		_, file, line, ok := runtime.Caller(2 + depth) // Code of interest is 2 levels up in stack
		if ok {
			file = filepath.Base(file)
		} else {
			file = "???"
			line = 0
		}

		entry.File = fmt.Sprintf("%s:%d", file, line)
	}

	// Append Level
	if config.Format&IncludeLevel != 0 {
		switch level {
		case LevelDebug:
			entry.Level = "debug"
		case LevelVerbose:
			entry.Level = "verbose"
		case LevelInfo:
			entry.Level = "info"
		case LevelWarn:
			entry.Level = "warn"
		case LevelError:
			entry.Level = "error"
		case LevelFatal:
			entry.Level = "fatal"
		case LevelPanic:
			entry.Level = "panic"
		}
	}

	if len(trace) > 0 {
		entry.Trace = trace
	}

	// Append actual log entry
	entry.Message = fmt.Sprintf(format, values...)

	// Convert to JSON
	line, err := json.Marshal(&entry)
	if err != nil {
		return err
	}

	// Write to output
	_, err = config.Output.Write(append(line, '\n'))

	if level == LevelFatal {
		os.Exit(1)
	}
	if level == LevelPanic {
		panic(entry)
	}
	return err
}

// Logs a text entry based on the system config
func (config *SystemConfig) logText(system string, level Level, depth int, trace, format string,
	values ...interface{}) error {

	if config.MinLevel > level {
		return nil // Level is below minimum
	}

	// Create log entry
	now := time.Now()
	entry := make([]byte, 0, 1024)

	// Append Date
	if config.Format&IncludeDate != 0 {
		year, month, day := now.Date()
		entry = append(entry, fmt.Sprintf("%04d/%02d/%02d ", year, month, day)...)
	}

	// Append Time
	if config.Format&IncludeTime != 0 {
		hour, min, sec := now.Clock()
		entry = append(entry, fmt.Sprintf("%02d:%02d:%02d", hour, min, sec)...)
		if config.Format&IncludeMicro == 0 {
			entry = append(entry, ' ')
		}
	}

	// Append microseconds
	if config.Format&IncludeMicro != 0 {
		if config.Format&IncludeTime != 0 {
			entry = append(entry, '.')
		}

		entry = append(entry, fmt.Sprintf("%06d", now.Nanosecond()/1e3)...)
		entry = append(entry, ' ')
	}

	// Append System
	if config.Format&IncludeSystem != 0 {
		entry = append(entry, '[')
		entry = append(entry, system...)
		entry = append(entry, ']')
		entry = append(entry, ' ')
	}

	// Append File
	if config.Format&IncludeFile != 0 {
		_, file, line, ok := runtime.Caller(2 + depth) // Code of interest is 2 levels up in stack
		if ok {
			file = filepath.Base(file)
		} else {
			file = "???"
			line = 0
		}

		entry = append(entry, file...)
		entry = append(entry, ':')
		entry = append(entry, fmt.Sprintf("%d ", line)...)
	}

	// Append Level
	if config.Format&IncludeLevel != 0 {
		switch level {
		case LevelDebug:
			entry = append(entry, []byte("Debug - ")...)
		case LevelVerbose:
			entry = append(entry, []byte("Verbose - ")...)
		case LevelInfo:
			entry = append(entry, []byte("Info - ")...)
		case LevelWarn:
			entry = append(entry, []byte("Warn - ")...)
		case LevelError:
			entry = append(entry, []byte("Error - ")...)
		case LevelFatal:
			entry = append(entry, []byte("Fatal - ")...)
		case LevelPanic:
			entry = append(entry, []byte("Panic - ")...)
		}
	}

	if len(trace) > 0 {
		entry = append(entry, '<')
		entry = append(entry, []byte(trace)...)
		entry = append(entry, []byte("> ")...)
	}

	// Append actual log entry
	entry = append(entry, fmt.Sprintf(format, values...)...)

	// Append new line
	if entry[len(entry)-1] != '\n' {
		entry = append(entry, '\n')
	}

	// Write to output
	_, err := config.Output.Write(entry)

	if level == LevelFatal {
		os.Exit(1)
	}
	if level == LevelPanic {
		panic(entry)
	}
	return err
}
