//go:build race

package app

// raceEnabled reports whether tests run under the race detector, which slows
// execution several-fold and makes wall-clock budgets meaningless.
const raceEnabled = true
