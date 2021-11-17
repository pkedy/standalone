package main

import (
	"log"

	"github.com/dapr/standalone"
)

var version = ""

func main() {
	if version == "" {
		log.Fatal("version is not set")
	}
	if err := standalone.Install(version); err != nil {
		log.Fatal(err)
	}
}
