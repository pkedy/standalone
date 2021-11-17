package standalone

import (
	"embed"
)

//go:embed cli/darwin_amd64/dapr_darwin_amd64.tar.gz
var cliBinary []byte

//go:embed binaries/darwin_amd64
var binaries embed.FS
