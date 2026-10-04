// Command testapp is the Grove test application binary that real-process
// tests build and start as Grovlets.
package main

import (
	"github.com/grove-project/grove/internal/testapp/runtimeapp"
	groveruntime "github.com/grove-project/grove/runtime"
)

func main() {
	groveruntime.Main(runtimeapp.RuntimeDefinition())
}
