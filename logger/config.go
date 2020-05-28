package logger

import (
	"os"
	"sync"
)

// Config defines the logging configuration for the context it is attached to.
type Config struct {
	Main               *SystemConfig
	IncludedSubSystems map[string]bool          // If true, log in main log
	SubSystems         map[string]*SystemConfig // SubSystem specific configs
	IsText             bool                     // If true, log is in plain text, otherwise it is JSON
	mutex              sync.Mutex
}

var DefaultConfig = Config{
	Main: &SystemConfig{
		Output:   os.Stdout,
		MinLevel: LevelInfo,
		Format:   IncludeDate | IncludeTime | IncludeFile | IncludeLevel,
	},
}

var emptyConfig = Config{
	Main: &SystemConfig{
		Output: nil,
	},
}

// Creates a new config with default production values.
//   Logs info level and above to stderr.
func NewProductionConfig() *Config {
	result := Config{
		IncludedSubSystems: make(map[string]bool),
		SubSystems:         make(map[string]*SystemConfig),
	}

	result.Main = NewProductionSystemConfig()
	return &result
}

// Creates a new config with default development values.
//   Logs debug level and above to stderr.
func NewDevelopmentConfig() *Config {
	result := Config{
		IncludedSubSystems: make(map[string]bool),
		SubSystems:         make(map[string]*SystemConfig),
	}

	result.Main = NewDevelopmentSystemConfig()
	return &result
}

// Enables a subsytem to log to the main log
func (config *Config) EnableSubSystem(subsystem string) {
	config.mutex.Lock()
	defer config.mutex.Unlock()

	config.IncludedSubSystems[subsystem] = true
}
