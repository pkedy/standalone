package standalone

import (
	"embed"
)

//go:embed cli/linux_amd64/dapr_linux_amd64.tar.gz
var cliBinary []byte

//go:embed binaries/linux_amd64
var binaries embed.FS
