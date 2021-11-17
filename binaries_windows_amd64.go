package standalone

import (
	"embed"
)

//go:embed cli/windows_amd64/dapr_windows_amd64.zip
var cliBinary []byte

//go:embed binaries/windows_amd64
var binaries embed.FS
