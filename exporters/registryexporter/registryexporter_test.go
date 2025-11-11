package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrepareRegistryMap(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		t.Setenv("QUAY_URL", "")
		assert.Panics(t, func() {
			PrepareRegistryMap()
		}, "Expected PrepareRegistryMap to panic when QUAY_URL is empty")
	})

	t.Run("default", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.json")

		auth := base64.StdEncoding.EncodeToString([]byte("testuser:testpass"))
		dockerConfig := DockerConfig{
			Auths: map[string]AuthConfig{
				"quay.io": {
					Auth: auth,
				},
			},
		}

		configData, err := json.Marshal(dockerConfig)
		assert.NoError(t, err)

		err = os.WriteFile(configPath, configData, 0644)
		assert.NoError(t, err)

		t.Setenv("QUAY_URL", "https://quay.io")
		t.Setenv("DOCKER_CONFIG", tmpDir)

		registryMap := PrepareRegistryMap()
		assert.IsType(t, map[string]RegistryConfig{}, registryMap)
		assert.NotNil(t, registryMap)
		assert.Equal(t, "https://quay.io", registryMap["quay.io"].URL)
		assert.Equal(t, "testuser", registryMap["quay.io"].Credentials.Username)
		assert.Equal(t, "testpass", registryMap["quay.io"].Credentials.Password)
	})
}
