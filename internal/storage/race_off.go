//go:build !race
// +build !race

package storage

// See race_on.go for what raceEnabled gates (DF-RABBIT-HOLE-013).
const raceEnabled = false
