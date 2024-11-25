package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pkg/errors"
)

// Config 是从配置文件读取的Env信息
type Config struct {
	Config SubConfigStruct `json:"config"`
}

type SubConfigStruct struct {
	Env []string `json:"Env"`
}

// Manifest 是从配置文件读取的 Layers 信息
type Manifest struct {
	Layers []Layer
}

// Layer 代表一个 Docker 镜像层
type Layer struct {
	Digest    string
	MediaType string
	Size      uint64
}

// loadConfig 加载 config.json 文件
func loadConfig(configPath string) ([]string, error) {
	file, err := os.Open(configPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var config Config
	err = json.NewDecoder(file).Decode(&config)
	if err != nil {
		return nil, err
	}

	return config.Config.Env, nil
}

// loadManifest 加载 manifest.json 文件
func loadManifest(manifestPath string) ([]Layer, error) {
	file, err := os.Open(manifestPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var manifest Manifest
	err = json.NewDecoder(file).Decode(&manifest)
	if err != nil {
		return nil, err
	}

	return manifest.Layers, nil
}

func mountRecPrivate() error {
	fmt.Println("mounting recursive private: mount --make-rprivate /")
	cmd := exec.Command("mount", "--make-rprivate", "/")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	return nil
}

// setEnv 设置环境变量
func setEnv(configPath string) error {
	// 读取环境变量并设置
	envVars, err := loadConfig(configPath)
	if err != nil {
		return errors.Wrap(err, "读取 config.json 时出错")
	}
	fmt.Println("setting env vars:", envVars)
	for _, e := range envVars {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			return errors.Errorf("无效的环境变量: %s", e)
		}
		err = os.Setenv(parts[0], parts[1])
		if err != nil {
			return errors.Wrap(err, "设置环境变量时出错")
		}
	}
	return nil
}

func mountTmpfs(targetDir string) error {
	fmt.Println("mounting tmpfs filesystem: mount -t tmpfs tmpfs", targetDir)
	cmd := exec.Command("mount", "-t", "tmpfs", "tmpfs", targetDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	return nil
}

func prepareDirs(baseDir string, dirs []string) error {
	err := os.MkdirAll(baseDir, os.ModePerm)
	if err != nil {
		return errors.Wrap(err, "创建 base 目录时出错")
	}
	err = mountTmpfs(baseDir)
	if err != nil {
		return errors.Wrap(err, "挂载 tmpfs 时出错")
	}
	fmt.Println("making dirs: mkdir -pv", dirs)
	for _, dir := range dirs {
		err = os.MkdirAll(dir, os.ModePerm)
		if err != nil {
			return errors.Wrapf(err, "创建 %s 目录时出错", dir)
		}
	}
	return nil
}

func setLayers(manifestPath, baseDir, targetDir string) error {
	// 读取 layers 信息
	layers, err := loadManifest(manifestPath)
	if err != nil {
		return errors.Wrap(err, "读取 manifest.json 时出错")
	}

	// 创建必要的目录
	upperDir := filepath.Join(baseDir, "upper")
	workDir := filepath.Join(baseDir, "work")
	prepareDirs(baseDir, []string{upperDir, workDir, targetDir})

	lowerDirs := []string{}
	// lower要求layers逆序挂载
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		layerPath := filepath.Join("/tmp/proxy_pool/layers", strings.Split(layer.Digest, ":")[1])
		lowerDirs = append(lowerDirs, layerPath)
	}

	// 挂载 overlay 文件系统
	err = mountOverlayFS(lowerDirs, upperDir, workDir, targetDir)
	if err != nil {
		return errors.Wrap(err, "挂载 overlay 文件系统时出错")
	}
	return nil
}

// mountOverlayFS 挂载 overlay 文件系统
func mountOverlayFS(lowerDirs []string, upperDir, workDir, targetDir string) error {
	lowerdir := strings.Join(lowerDirs, ":")
	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerdir, upperDir, workDir)

	fmt.Println("mounting overlay filesystem: mount -t overlay overlay -o", options, targetDir)

	// 调用系统 mount 命令
	cmd := exec.Command("mount", "-t", "overlay", "overlay", "-o", options, targetDir)
	output, err := cmd.CombinedOutput() // 获取命令输出
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	return nil
}

func mountBaseFs(targetDir string) error {
	fmt.Println("mounting proc filesystem: mount -t proc none", filepath.Join(targetDir, "proc"))
	cmd := exec.Command("mount", "-t", "proc", "none", filepath.Join(targetDir, "proc"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	fmt.Println("mounting sys filesystem: mount -t sysfs none", filepath.Join(targetDir, "sys"))
	cmd = exec.Command("mount", "-t", "sysfs", "none", filepath.Join(targetDir, "sys"))
	output, err = cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	fmt.Println("mounting dev filesystem: mount -t devtmpfs devtmpfs", filepath.Join(targetDir, "dev"))
	cmd = exec.Command("mount", "-t", "devtmpfs", "devtmpfs", filepath.Join(targetDir, "dev"))
	output, err = cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	fmt.Println("mounting devpts filesystem: mount -t devpts devpts", filepath.Join(targetDir, "dev/pts"))
	cmd = exec.Command("mount", "-t", "devpts", "devpts", filepath.Join(targetDir, "dev/pts"))
	output, err = cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	fmt.Println("mounting shm filesystem: mount -t tmpfs shm", filepath.Join(targetDir, "dev/shm"))
	cmd = exec.Command("mount", "-t", "tmpfs", "shm", filepath.Join(targetDir, "dev/shm"))
	output, err = cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	fmt.Println("mounting run filesystem: mount -t tmpfs tmpfs", filepath.Join(targetDir, "run"))
	cmd = exec.Command("mount", "-t", "tmpfs", "tmpfs", filepath.Join(targetDir, "run"))
	output, err = cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	fmt.Println("mounting tmp filesystem: mount -t tmpfs tmpfs", filepath.Join(targetDir, "tmp"))
	cmd = exec.Command("mount", "-t", "tmpfs", "tmpfs", filepath.Join(targetDir, "tmp"))
	output, err = cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	return nil
}

func mountVolume(volumeDir, targetDir string) error {
	if _, err := os.Stat(volumeDir); os.IsNotExist(err) {
		return errors.Wrap(err, "volume 目录不存在")
	}
	targetVolumeDir := filepath.Join(targetDir, "volume")
	err := os.MkdirAll(targetVolumeDir, os.ModePerm)
	if err != nil {
		return errors.Wrap(err, "创建 volume 目录时出错")
	}
	fmt.Println("mounting volume filesystem: mount --bind", volumeDir, targetVolumeDir)
	cmd := exec.Command("mount", "--bind", volumeDir, targetVolumeDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "mount output: %s", string(output))
	}
	return nil
}

func chroot(targetDir string) error {
	fmt.Println("change current dir :", "cd", targetDir)
	if err := os.Chdir(targetDir); err != nil {
		return errors.Wrap(err, "chdir 时出错")
	}
	fmt.Println("change rootfs: pivot_root . .")
	if err := syscall.PivotRoot(".", "."); err != nil {
		return errors.Wrap(err, "pivot_root 时出错")
	}
	fmt.Println("unmounting old root: umount -l .")
	cmd := exec.Command("umount", "-l", ".")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "umount output: %s", string(output))
	}
	return nil
}

// runInNamespace 启动子进程并在隔离的 namespace 和 chroot 环境中运行
func runInNamespace(configPath, manifestPath, baseDir, volumeDir string) error {
	cmd := exec.Command("/proc/self/exe", append([]string{"child"}, configPath, manifestPath, baseDir, volumeDir)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 设置子进程的 SysProcAttr，进入新的 namespaces
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWIPC |
			syscall.CLONE_NEWNET |
			syscall.CLONE_NEWNS |
			syscall.CLONE_NEWPID,
	}

	return cmd.Run()
}

// childProcess 处理子进程的逻辑
func childProcess(configPath, manifestPath, baseDir, volumeDir string) {
	err := mountRecPrivate()
	if err != nil {
		fmt.Printf("mountRecPrivate 时出错: %v\n", err)
		return
	}
	err = setEnv(configPath)
	if err != nil {
		fmt.Printf("设置环境变量时出错: %v\n", err)
		return
	}

	targetDir := filepath.Join(baseDir, "merged")
	err = setLayers(manifestPath, baseDir, targetDir)
	if err != nil {
		fmt.Printf("设置 layers 时出错: %v\n", err)
		return
	}

	err = mountBaseFs(targetDir)
	if err != nil {
		fmt.Printf("挂载基础文件系统时出错: %v\n", err)
		return
	}

	err = mountVolume(volumeDir, targetDir)
	if err != nil {
		fmt.Printf("挂载 volume 时出错: %v\n", err)
		return
	}

	err = chroot(targetDir)
	if err != nil {
		fmt.Printf("chroot 时出错: %v\n", err)
		return
	}

	// 启动 sh shell 并连接标准输入输出
	cmd := exec.Command("/bin/sh")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("运行 /bin/sh 时出错: %v\n", err)
	}
}

func main() {
	// 如果参数包含 "child"，则进入子进程逻辑
	if len(os.Args) > 1 && os.Args[1] == "child" {
		if len(os.Args) != 6 {
			fmt.Println("Invalid number of arguments for child process")
			os.Exit(1)
		}
		childProcess(os.Args[2], os.Args[3], os.Args[4], os.Args[5])
		return
	}

	configPath := "/tmp/proxy_pool/config.json"
	manifestPath := "/tmp/proxy_pool/manifest.json"
	baseDir := "/tmp/proxy_pool/overlay"
	volumeDir := "/tmp/proxy_pool/volume"

	err := os.MkdirAll(volumeDir, os.ModePerm)
	if err != nil {
		fmt.Printf("创建 volume 目录时出错: %v\n", err)
		return
	}

	// 切换到隔离的 namespace 和 chroot 环境中运行
	err = runInNamespace(configPath, manifestPath, baseDir, volumeDir)
	if err != nil {
		fmt.Printf("在 namespace 和 chroot 环境中运行时出错: %v\n", err)
		return
	}
}
