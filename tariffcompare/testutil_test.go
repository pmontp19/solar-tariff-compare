package tariffcompare

import "os"

// testEnv llegeix una variable d'entorn (per tests).
func testEnv(key string) string { return os.Getenv(key) }
