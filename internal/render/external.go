package render

// externalTurns is installed by an optional, build-tagged agent adapter to
// supply recent-turn text for its kind. Nil in the default build.
var externalTurns func(kind, key string, n int) ([][2]string, bool)

// RegisterExternalTurns installs the recent-turns hook for an external kind.
func RegisterExternalTurns(f func(kind, key string, n int) ([][2]string, bool)) {
	externalTurns = f
}
