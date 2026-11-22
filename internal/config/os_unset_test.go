package config

import "os"

func osUnset(key string) { os.Unsetenv(key) }
