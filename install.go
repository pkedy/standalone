package standalone

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v2"
)

const (
	daprDefaultHost     = "localhost"
	daprDockerImageName = "daprio/dapr"

	// DaprPlacementContainerName is the container name of placement service.
	DaprPlacementContainerName = "dapr_placement"
	// DaprRedisContainerName is the container name of redis.
	DaprRedisContainerName = "dapr_redis"
	// DaprZipkinContainerName is the container name of zipkin.
	DaprZipkinContainerName = "dapr_zipkin"
)

//go:embed images
var images embed.FS

var osarch = fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

func Install(version string) error {
	fmt.Printf("Installing Dapr %s\n", version)
	homedir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	versionNum := strings.TrimPrefix(version, "v")

	daprHomeDir := filepath.Join(homedir, ".dapr")
	daprCompDir := filepath.Join(daprHomeDir, "components")
	if err = os.MkdirAll(daprCompDir, 0775); err != nil {
		return err
	}
	daprBinDir := filepath.Join(daprHomeDir, "bin")
	if err = os.MkdirAll(daprBinDir, 0775); err != nil {
		return err
	}

	fmt.Println("Installing CLI...")
	if runtime.GOOS == "windows" {
		_, err = unzip(bytes.NewReader(cliBinary), int64(len(cliBinary)), daprBinDir)
	} else {
		err = extractTarGz(bytes.NewReader(cliBinary), daprBinDir)
	}
	if err != nil {
		return fmt.Errorf("could not install CLI: %w", err)
	}
	daprExeName := "dapr"
	if runtime.GOOS == "windows" {
		daprExeName += ".exe"
	}

	configPath := filepath.Join(daprHomeDir, "config.yaml")
	if err = createDefaultConfiguration(daprDefaultHost, configPath); err != nil {
		return err
	}
	if err = createRedisPubSub(daprDefaultHost, daprCompDir); err != nil {
		return err
	}
	if err = createRedisStateStore(daprDefaultHost, daprCompDir); err != nil {
		return err
	}

	fmt.Println("Installing binaries...")
	// The embed package does not use path separators of the OS.
	// Using filepath.Join does not work.
	// https://github.com/golang/go/issues/44305
	dir := path.Join("binaries", osarch)
	entries, err := binaries.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("could not binary root for %s: %w", osarch, err)
	}
	for _, e := range entries {
		fmt.Printf("  • %s\n", e.Name())
		f, err := binaries.Open(path.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("could not open file %s: %w", e.Name(), err)
		}

		ext := strings.ToLower(filepath.Ext(e.Name()))
		switch ext {
		case ".zip":
			var fi fs.FileInfo
			fi, err = f.Stat()
			if err != nil {
				return err
			}
			var fileBytes []byte
			fileBytes, err = io.ReadAll(f)
			if err == nil {
				_, err = unzip(bytes.NewReader(fileBytes), fi.Size(), daprBinDir)
			}
		case ".tar.gz", ".gz":
			err = extractTarGz(f, daprBinDir)
		default:
			fmt.Printf("Unknown ext: %s\n", ext)
		}
		if err != nil {
			return err
		}
		f.Close()
	}

	fmt.Println("Loading docker images...")
	entries, err = images.ReadDir("images")
	if err != nil {
		return err
	}
	for _, e := range entries {
		fmt.Printf("  • %s... ", e.Name())
		f, err := images.Open(path.Join("images", e.Name()))
		if err != nil {
			return err
		}

		if err := dockerLoad(f); err != nil {
			return err
		}
		f.Close()
	}

	dockerNetwork := ""

	if err := removeDockerContainer(DaprPlacementContainerName, dockerNetwork); err != nil {
		return fmt.Errorf("could not stop previously installed placement service: %w", err)
	}

	fmt.Println("Starting docker containers...")
	fmt.Println("  • Dapr placement service")
	if err := runPlacementService(versionNum, dockerNetwork); err != nil {
		return fmt.Errorf("could not start placement service: %w", err)
	}
	fmt.Println("  • redis")
	if err := runRedis(dockerNetwork); err != nil {
		return fmt.Errorf("could not start redis: %w", err)
	}
	fmt.Println("  • openzipkin/zipkin")
	if err := runZipkin(dockerNetwork); err != nil {
		return fmt.Errorf("could not start zipkin: %w", err)
	}

	fmt.Println()
	fmt.Println("Success!")
	fmt.Printf("The Dapr CLI was installed to %s/%s.\n", daprBinDir, daprExeName)
	fmt.Printf("You may want to add %s to your PATH or copy %s to a PATH location.\n", daprBinDir, daprExeName)
	if runtime.GOOS != "windows" {
		fmt.Printf("e.g. > sudo cp %s/%s /usr/local/bin\n", daprBinDir, daprExeName)
	}
	fmt.Println()

	return nil
}

func dockerLoad(in io.Reader) error {
	subProcess := exec.Command("docker", "load")

	stdin, err := subProcess.StdinPipe()
	if err != nil {
		return fmt.Errorf("an error occured: %w", err)
	}
	defer stdin.Close()

	subProcess.Stdout = os.Stdout
	subProcess.Stderr = os.Stderr

	if err = subProcess.Start(); err != nil {
		return fmt.Errorf("an error occured: %w", err)
	}

	if _, err = io.Copy(stdin, in); err != nil {
		return fmt.Errorf("an error occured: %w", err)
	}

	stdin.Close()

	if err = subProcess.Wait(); err != nil {
		return fmt.Errorf("an error occured: %w", err)
	}

	return nil
}

func extractTarGz(gzipStream io.Reader, base string) error {
	uncompressedStream, err := gzip.NewReader(gzipStream)
	if err != nil {
		return err
	}

	tarReader := tar.NewReader(uncompressedStream)

	removePath := filepath.Join("release", osarch)

	for {
		header, err := tarReader.Next()

		if err == io.EOF {
			break
		}

		if err != nil {
			return fmt.Errorf("extractTarGz: Next() failed: %w", err)
		}

		p := filepath.Join(base, header.Name)
		p = strings.Replace(p, removePath, "", 1)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			outFile, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, header.FileInfo().Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				return err
			}
			outFile.Close()

		default:
			return fmt.Errorf(
				"extractTarGz: uknown type: %b in %s",
				header.Typeflag,
				header.Name)
		}
	}

	return nil
}

func unzip(src io.ReaderAt, size int64, dest string) ([]string, error) {
	var filenames []string

	r, err := zip.NewReader(src, size)
	if err != nil {
		return filenames, err
	}

	removePath := filepath.Join("release", osarch)

	for _, f := range r.File {
		// Store filename/path for returning and using later on
		fpath := filepath.Join(dest, f.Name)
		fpath = strings.Replace(fpath, removePath, "", 1)

		// Check for ZipSlip. More Info: http://bit.ly/2MsjAWE
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return filenames, fmt.Errorf("%s: illegal file path", fpath)
		}

		filenames = append(filenames, fpath)

		if f.FileInfo().IsDir() {
			// Make Folder
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		// Make File
		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return filenames, err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return filenames, err
		}

		rc, err := f.Open()
		if err != nil {
			return filenames, err
		}

		_, err = io.Copy(outFile, rc)

		// Close the file without defer to close before next iteration of loop
		outFile.Close()
		rc.Close()

		if err != nil {
			return filenames, err
		}
	}

	return filenames, nil
}

const (
	pubSubYamlFileName     = "pubsub.yaml"
	stateStoreYamlFileName = "statestore.yaml"
)

type configuration struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Tracing struct {
			SamplingRate string `yaml:"samplingRate,omitempty"`
			Zipkin       struct {
				EndpointAddress string `yaml:"endpointAddress,omitempty"`
			} `yaml:"zipkin,omitempty"`
		} `yaml:"tracing,omitempty"`
	} `yaml:"spec"`
}

type component struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Type     string                  `yaml:"type"`
		Version  string                  `yaml:"version"`
		Metadata []componentMetadataItem `yaml:"metadata"`
	} `yaml:"spec"`
}

type componentMetadataItem struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

func createRedisStateStore(redisHost string, componentsPath string) error {
	redisStore := component{
		APIVersion: "dapr.io/v1alpha1",
		Kind:       "Component",
	}

	redisStore.Metadata.Name = "statestore"
	redisStore.Spec.Type = "state.redis"
	redisStore.Spec.Version = "v1"
	redisStore.Spec.Metadata = []componentMetadataItem{
		{
			Name:  "redisHost",
			Value: fmt.Sprintf("%s:6379", redisHost),
		},
		{
			Name:  "redisPassword",
			Value: "",
		},
		{
			Name:  "actorStateStore",
			Value: "true",
		},
	}

	b, err := yaml.Marshal(&redisStore)
	if err != nil {
		return err
	}

	filePath := filepath.Join(componentsPath, stateStoreYamlFileName)
	err = checkAndOverWriteFile(filePath, b)

	return err
}

func createRedisPubSub(redisHost string, componentsPath string) error {
	redisPubSub := component{
		APIVersion: "dapr.io/v1alpha1",
		Kind:       "Component",
	}

	redisPubSub.Metadata.Name = "pubsub"
	redisPubSub.Spec.Type = "pubsub.redis"
	redisPubSub.Spec.Version = "v1"
	redisPubSub.Spec.Metadata = []componentMetadataItem{
		{
			Name:  "redisHost",
			Value: fmt.Sprintf("%s:6379", redisHost),
		},
		{
			Name:  "redisPassword",
			Value: "",
		},
	}

	b, err := yaml.Marshal(&redisPubSub)
	if err != nil {
		return err
	}

	filePath := filepath.Join(componentsPath, pubSubYamlFileName)
	err = checkAndOverWriteFile(filePath, b)

	return err
}

func createDefaultConfiguration(zipkinHost, filePath string) error {
	defaultConfig := configuration{
		APIVersion: "dapr.io/v1alpha1",
		Kind:       "Configuration",
	}
	defaultConfig.Metadata.Name = "daprConfig"
	if zipkinHost != "" {
		defaultConfig.Spec.Tracing.SamplingRate = "1"
		defaultConfig.Spec.Tracing.Zipkin.EndpointAddress = fmt.Sprintf("http://%s:9411/api/v2/spans", zipkinHost)
	}
	b, err := yaml.Marshal(&defaultConfig)
	if err != nil {
		return err
	}

	err = checkAndOverWriteFile(filePath, b)

	return err
}

func checkAndOverWriteFile(filePath string, b []byte) error {
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		// #nosec G306
		if err = ioutil.WriteFile(filePath, b, 0644); err != nil {
			return err
		}
	}
	return nil
}

func removeDockerContainer(containerName, network string) error {
	container := createContainerName(containerName, network)
	exists, _ := confirmContainerIsRunningOrExists(container, false)
	if !exists {
		return nil
	}
	fmt.Printf("Removing container: %s\n", container)
	_, err := RunCmdAndWait(
		"docker", "rm",
		"--force",
		container)

	return err
}

func runPlacementService(version string, dockerNetwork string) error {
	placementContainerName := createContainerName(DaprPlacementContainerName, dockerNetwork)

	image := fmt.Sprintf("%s:%s", daprDockerImageName, version)

	exists, err := confirmContainerIsRunningOrExists(placementContainerName, false)
	if err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%s container exists or is running", placementContainerName)
	}

	args := []string{
		"run",
		"--name", placementContainerName,
		"--restart", "always",
		"-d",
		"--entrypoint", "./placement",
	}

	if dockerNetwork != "" {
		args = append(args,
			"--network", dockerNetwork,
			"--network-alias", DaprPlacementContainerName)
	} else {
		osPort := 50005
		if runtime.GOOS == "windows" {
			osPort = 6050
		}

		args = append(args,
			"-p", fmt.Sprintf("%v:50005", osPort))
	}

	args = append(args, image)

	_, err = RunCmdAndWait("docker", args...)

	if err != nil {
		runError := isContainerRunError(err)
		if !runError {
			return parseDockerError("placement service", err)
		} else {
			return fmt.Errorf("docker %s failed with: %v", args, err)
		}
	}

	return nil
}

func runZipkin(dockerNetwork string) error {
	zipkinContainerName := createContainerName(DaprZipkinContainerName, dockerNetwork)

	exists, err := confirmContainerIsRunningOrExists(zipkinContainerName, false)
	if err != nil {
		return err
	}
	args := []string{}

	if exists {
		// do not create container again if it exists
		args = append(args, "start", zipkinContainerName)
	} else {
		args = append(args,
			"run",
			"--name", zipkinContainerName,
			"--restart", "always",
			"-d",
		)

		if dockerNetwork != "" {
			args = append(
				args,
				"--network", dockerNetwork,
				"--network-alias", DaprZipkinContainerName)
		} else {
			args = append(
				args,
				"-p", "9411:9411")
		}

		args = append(args, "openzipkin/zipkin")
	}
	_, err = RunCmdAndWait("docker", args...)

	if err != nil {
		runError := isContainerRunError(err)
		if !runError {
			return parseDockerError("Zipkin tracing", err)
		} else {
			return fmt.Errorf("docker %s failed with: %v", args, err)
		}
	}

	return nil
}

func runRedis(dockerNetwork string) error {
	redisContainerName := createContainerName(DaprRedisContainerName, dockerNetwork)

	exists, err := confirmContainerIsRunningOrExists(redisContainerName, false)
	if err != nil {
		return err
	}
	args := []string{}

	if exists {
		// do not create container again if it exists
		args = append(args, "start", redisContainerName)
	} else {
		args = append(args,
			"run",
			"--name", redisContainerName,
			"--restart", "always",
			"-d",
		)

		if dockerNetwork != "" {
			args = append(
				args,
				"--network", dockerNetwork,
				"--network-alias", DaprRedisContainerName)
		} else {
			args = append(
				args,
				"-p", "6379:6379")
		}

		args = append(args, "redis")
	}
	_, err = RunCmdAndWait("docker", args...)

	if err != nil {
		runError := isContainerRunError(err)
		if !runError {
			return parseDockerError("Redis state store", err)
		} else {
			return fmt.Errorf("docker %s failed with: %v", args, err)
		}
	}

	return nil
}

// check if the container either exists and stopped or is running.
func confirmContainerIsRunningOrExists(containerName string, isRunning bool) (bool, error) {
	// e.g. docker ps --filter name=dapr_redis --filter status=running --format {{.Names}}

	args := []string{"ps", "--all", "--filter", "name=" + containerName}

	if isRunning {
		args = append(args, "--filter", "status=running")
	}

	args = append(args, "--format", "{{.Names}}")
	response, err := RunCmdAndWait("docker", args...)
	response = strings.TrimSuffix(response, "\n")

	// If 'docker ps' failed due to some reason
	if err != nil {
		return false, fmt.Errorf("unable to confirm whether %s is running or exists. error\n%v", containerName, err.Error())
	}
	// 'docker ps' worked fine, but the response did not have the container name
	if response == "" || response != containerName {
		if isRunning {
			return false, fmt.Errorf("container %s is not running", containerName)
		}
		return false, nil
	}

	return true, nil
}

func parseDockerError(component string, err error) error {
	if exitError, ok := err.(*exec.ExitError); ok {
		exitCode := exitError.ExitCode()
		if exitCode == 125 { // see https://github.com/moby/moby/pull/14012
			return fmt.Errorf("failed to launch %s. Is it already running?", component)
		}
		if exitCode == 127 {
			return fmt.Errorf("failed to launch %s. Make sure Docker is installed and running", component)
		}
	}
	return err
}

func isContainerRunError(err error) bool {
	if exitError, ok := err.(*exec.ExitError); ok {
		exitCode := exitError.ExitCode()
		return exitCode == 125
	}
	return false
}

func createContainerName(serviceContainerName string, dockerNetwork string) string {
	if dockerNetwork != "" {
		return fmt.Sprintf("%s_%s", serviceContainerName, dockerNetwork)
	}

	return serviceContainerName
}

func RunCmdAndWait(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	err = cmd.Start()
	if err != nil {
		return "", err
	}

	resp, err := ioutil.ReadAll(stdout)
	if err != nil {
		return "", err
	}
	errB, err := ioutil.ReadAll(stderr)
	if err != nil {
		return "", nil
	}

	err = cmd.Wait()
	if err != nil {
		// in case of error, capture the exact message
		if len(errB) > 0 {
			return "", errors.New(string(errB))
		}
		return "", err
	}

	return string(resp), nil
}
