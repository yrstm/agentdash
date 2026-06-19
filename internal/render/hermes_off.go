//go:build !hermes

package render

// updateTags is empty in the default build: the reinstall hint advertises the
// plain `go install` (the Hermes build overrides this in hermes_on.go).
const updateTags = ""
