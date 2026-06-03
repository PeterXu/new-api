package common

import (
	"fmt"
	"os"
	"strconv"
)

func GetEnvOrDefault(env string, defaultValue int) int {
	if env == "" || os.Getenv(env) == "" {
		return defaultValue
	}
	num, err := strconv.Atoi(os.Getenv(env))
	if err != nil {
		SysError(fmt.Sprintf("failed to parse %s: %s, using default value: %d", env, err.Error(), defaultValue))
		return defaultValue
	}
	return num
}

func GetEnvOrDefaultString(env string, defaultValue string) string {
	if env == "" || os.Getenv(env) == "" {
		return defaultValue
	}
	return os.Getenv(env)
}

func GetEnvOrDefaultBool(env string, defaultValue bool) bool {
	if env == "" || os.Getenv(env) == "" {
		return defaultValue
	}
	b, err := strconv.ParseBool(os.Getenv(env))
	if err != nil {
		SysError(fmt.Sprintf("failed to parse %s: %s, using default value: %t", env, err.Error(), defaultValue))
		return defaultValue
	}
	return b
}

// GetLogInterval returns a log interval from env variable.
// 0 means logging is disabled, positive value means log every N cycles.
func GetLogInterval(envKey string, defaultValue int) int {
	logInterval := GetEnvOrDefaultString(envKey, strconv.Itoa(defaultValue))
	if logInterval == "0" {
		return 0
	}
	if v, err := strconv.Atoi(logInterval); err == nil && v > 0 {
		return v
	}
	return defaultValue
}
