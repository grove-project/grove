package main

import (
	"github.com/grove-project/grove/internal/testapp/runtimeapp"
	groveruntime "github.com/grove-project/grove/runtime"
)

func main() {
	groveruntime.Main(runtimeapp.RuntimeDefinition())
}
