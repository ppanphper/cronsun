package cronsun

import (
	"fmt"
	"runtime"
)

const VersionNumber = "0.4.0"

var (
	Version = fmt.Sprintf("v%s (build %s)", VersionNumber, runtime.Version())
)
