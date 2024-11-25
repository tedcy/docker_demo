package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"

	"github.com/containerd/containerd/archive/compression"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/pkg/errors"
)

type ConverterConfig struct {
	Source string
	Path   string
}

type Image struct {
	Ref name.Reference
	Img v1.Image
}

func createImage(config *ConverterConfig) (*Image, error) {
	ref, err := name.ParseReference(config.Source)
	if err != nil {
		return nil, errors.Wrap(err, "parse source reference")
	}
	image, err := remote.Image(
		ref,
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithPlatform(v1.Platform{
			Architecture: runtime.GOARCH,
			OS:           runtime.GOOS,
		}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "fetch source image")
	}
	return &Image{
		Ref: ref,
		Img: image,
	}, nil
}

func extractLayer(config *ConverterConfig, layer v1.Layer) error {
	hash, err := layer.Digest()
	if err != nil {
		return err
	}
	layerTarPath := path.Join(config.Path, "layers", hash.Hex+".tar")
	extractDir := path.Join(config.Path, "layers", hash.Hex)
	err = os.MkdirAll(extractDir, os.ModePerm)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("create layer directory %s", hash.String()))
	}
	cmd := exec.Command("tar", "-xf", layerTarPath, "-C", extractDir)
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, fmt.Sprintf("extract layer %s", hash.String()))
	}
	return nil
}

func pullLayer(config *ConverterConfig, layer v1.Layer) error {
	hash, err := layer.Digest()
	if err != nil {
		return err
	}
	// Pull the layer from source, we need to retry in case of
	// the layer is compressed or uncompressed
	var reader io.ReadCloser
	reader, err = layer.Compressed()
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("layer %s Compressed", hash.String()))
	}
	ds, err := compression.DecompressStream(reader)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("decompress layer %s", hash.String()))
	}
	defer ds.Close()
	layerTarPath := path.Join(config.Path, "layers", hash.Hex+".tar")
	layerTarDir := filepath.Dir(layerTarPath)
	err = os.MkdirAll(layerTarDir, os.ModePerm)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("create layer directory %s", hash.String()))
	}
	file, err := os.Create(layerTarPath)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("write layer %s create file", hash.String()))
	}
	defer file.Close()
	_, err = io.Copy(file, ds)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("write layer %s to file", hash.String()))
	}
	return nil
}

func pullLayers(config *ConverterConfig, image *Image) error {
	layers, err := image.Img.Layers()
	if err != nil {
		return errors.Wrap(err, "get image layers")
	}
	for _, layer := range layers {
		err = pullLayer(config, layer)
		if err != nil {
			return errors.Wrap(err, "pull image layer")
		}
		err = extractLayer(config, layer)
		if err != nil {
			return errors.Wrap(err, "extract image layer")
		}
	}
	return nil
}

func createManifest(config *ConverterConfig, image *Image) error {
	manifest, err := image.Img.RawManifest()
	if err != nil {
		return errors.Wrap(err, "get image manifest")
	}
	manifestPath := path.Join(config.Path, "manifest.json")
	file, err := os.Create(manifestPath)
	if err != nil {
		return errors.Wrap(err, "create manifest file")
	}
	defer file.Close()
	_, err = file.Write(manifest)
	if err != nil {
		return errors.Wrap(err, "write manifest file")
	}
	return nil
}

func createConfig(config *ConverterConfig, image *Image) error {
	configFile, err := image.Img.RawConfigFile()
	if err != nil {
		return errors.Wrap(err, "get image config")
	}
	configPath := path.Join(config.Path, "config.json")
	file, err := os.Create(configPath)
	if err != nil {
		return errors.Wrap(err, "create config file")
	}
	defer file.Close()
	_, err = file.Write(configFile)
	if err != nil {
		return errors.Wrap(err, "write config file")
	}
	return nil
}

func convert(config *ConverterConfig) error {
	image, err := createImage(config)
	if err != nil {
		return err
	}
	// err = pullLayers(config, image)
	// if err != nil {
	// 	return err
	// }
	err = createManifest(config, image)
	if err != nil {
		return err
	}
	err = createConfig(config, image)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	err := convert(&ConverterConfig{
		Source: "dockerpull.org/tedcy/proxy_pool",
		Path:   "/tmp/proxy_pool",
	})
	if err != nil {
		fmt.Println(err)
	}
}
