// Package buildinfo formats the --version line shared by the commands.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Version returns e.g. "pc-fe v0.2.0 (go1.25.1)". The version is the main
// module's, "(devel)" for a local build.
func Version(name string) string {
	v := "(devel)"
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		v = bi.Main.Version
	}
	return name + " " + v + " (" + runtime.Version() + ")"
}
