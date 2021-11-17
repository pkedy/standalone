package standalone

import (
	"embed"
)

//go:embed cli/linux_arm64/dapr_linux_arm64.tar.gz
var cliBinary []byte

//go:embed binaries/linux_amd64
var binaries embed.FS
