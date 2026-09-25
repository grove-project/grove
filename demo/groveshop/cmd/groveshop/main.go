package main

import (
	"github.com/grove-project/grove/demo/groveshopapp"
	groveruntime "github.com/grove-project/grove/runtime"
)

func main() {
	groveruntime.Main(groveshopapp.RuntimeDefinition())
}
