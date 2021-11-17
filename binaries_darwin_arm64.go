package standalone

import (
	"embed"
)

//go:embed cli/darwin_arm64/dapr_darwin_arm64.tar.gz
var cliBinary []byte

//go:embed binaries/darwin_arm64
var binaries embed.FS
