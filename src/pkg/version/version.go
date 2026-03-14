package version

import (
	"fmt"
	"runtime"
)

var (
	Version     = "dev"
	Commit      = "none"
	LicenseType = "Community"
)

func Print() {
	fmt.Printf("Emdexer Version: %s\n", Version)
	fmt.Printf("Git Commit:      %s\n", Commit)
	fmt.Printf("License:         %s\n", LicenseType)
	fmt.Printf("Go Version:      %s\n", runtime.Version())
	fmt.Printf("OS/Arch:         %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func IsEnterprise() bool {
	return LicenseType == "Enterprise"
}
