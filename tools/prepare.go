package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var version = ""

type (
	Config struct {
		Releases map[string]Release `json:"releases"`
	}

	Release struct {
		CLI      map[string]string   `json:"cli"`
		Binaries map[string][]string `json:"binaries"`
		Images   []string            `json:"images"`
	}
)

func main() {
	if version == "" {
		log.Fatal("version is not set")
	}
	if err := prepare(version); err != nil {
		log.Fatal(err)
	}
}

func prepare(version string) error {
	configBytes, err := os.ReadFile("releases.json")
	if err != nil {
		return err
	}

	var config Config
	if err = json.Unmarshal(configBytes, &config); err != nil {
		return err
	}

	release := config.Releases[version]

	fmt.Println("Saving images...")
	if err = os.MkdirAll("images", 0775); err != nil {
		return err
	}
	for _, image := range release.Images {
		if err = execute("docker", "pull", image); err != nil {
			return err
		}
		filename := image + ".tar.gz"
		filename = strings.ReplaceAll(filename, "/", "-")
		filename = strings.ReplaceAll(filename, ":", "-")
		if err = execute("docker", "save", "-o", filepath.Join("images", filename), image); err != nil {
			return err
		}
	}

	fmt.Println("Downloading cli...")
	for osarch, cliURL := range release.CLI {
		osarchDir := filepath.Join("cli", osarch)
		if err = os.MkdirAll(osarchDir, 0775); err != nil {
			return err
		}

		fmt.Println(cliURL)
		filename := filepath.Base(cliURL)
		target := filepath.Join(osarchDir, filename)
		if err = downloadFile(target, cliURL); err != nil {
			return err
		}
	}

	fmt.Println("Downloading binaries...")
	for osarch, binaries := range release.Binaries {
		osarchDir := filepath.Join("binaries", osarch)
		if err = os.MkdirAll(osarchDir, 0775); err != nil {
			return err
		}

		for _, binaryURL := range binaries {
			fmt.Println(binaryURL)
			filename := filepath.Base(binaryURL)
			target := filepath.Join(osarchDir, filename)
			if err = downloadFile(target, binaryURL); err != nil {
				return err
			}
		}
	}

	return nil
}

func execute(prog string, args ...string) error {
	cmd := exec.Command(prog, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func downloadFile(filepath string, url string) error {
	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}
