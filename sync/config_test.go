package sync

import (
	"github.com/stretchr/testify/assert"
	"path/filepath"
	"runtime"
	"testing"
)

func loadTestConfigWithError(testFileName string, t *testing.T) (Config, error) {
	// load configuration
	_, filename, _, _ := runtime.Caller(0)
	parent := filepath.Dir(filename)
	parent, _ = filepath.Abs(parent)
	testdata := filepath.Join(parent, "testdata")
	configPath := filepath.Join(testdata, testFileName)
	return LoadConfig(configPath)
}

func loadTestConfig(testFileName string, t *testing.T) Config {
	config, err := loadTestConfigWithError(testFileName, t)
	if err != nil {
		t.Errorf("Failed to load config file: %s", err)
		t.Fail()
	}
	return config
}

func TestBasicConfig(t *testing.T) {
	config := loadTestConfig("basic.yml", t)

	// create assertions on config
	a := assert.New(t)
	a.Equal(1, len(config.Realms))
	realm := config.Realms[0]
	a.Equal("sso", realm.Name)
	a.Equal(true, realm.SslVerify)
	a.Equal("client", realm.ClientId)
	a.Equal("secret", realm.ClientSecret)
}

func TestRealmMissingName(t *testing.T) {
	// create assertions on config
	a := assert.New(t)

	_, err := loadTestConfigWithError("bad_no_realm_name.yml", t)
	a.Error(err)
}

func TestEmptyRealmsOk(t *testing.T) {
	// create assertions on config
	a := assert.New(t)

	_, err := loadTestConfigWithError("empty_realms_ok.yml", t)
	a.Nil(err)
}
