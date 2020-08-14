package sync

import (
	"github.com/stretchr/testify/assert"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func loadTestGroupFile(testFileName string, t *testing.T) io.Reader {
	// load configuration
	_, filename, _, _ := runtime.Caller(0)
	parent := filepath.Dir(filename)
	parent, _ = filepath.Abs(parent)
	testdata := filepath.Join(parent, "testdata")
	testFile := filepath.Join(testdata, testFileName)
	reader, err := os.Open(testFile)
	if err != nil {
		t.Errorf("could not open test data file: %s", err)
		t.Fail()
	}
	return reader
}

func testOpenShiftGroupList(syncGroups map[string]Group, err error, t *testing.T) {
	a := assert.New(t)

	// do not expect any error
	a.Nil(err)

	// begin looking at groups
	a.Equal(2, len(syncGroups))
}

func TestOpenShiftGroupListYAML(t *testing.T) {
	syncGroups, err := GetOpenShiftGroupsFromReader(SyncConfig{}, loadTestGroupFile("input-group-list.yml", t))
	testOpenShiftGroupList(syncGroups, err, t)
}

func TestOpenShiftGroupListJSON(t *testing.T) {
	syncGroups, err := GetOpenShiftGroupsFromReader(SyncConfig{}, loadTestGroupFile("input-group-list.json", t))
	testOpenShiftGroupList(syncGroups, err, t)
}

func testOpenShiftSingleGroup(syncGroups map[string]Group, err error, t *testing.T) {
	a := assert.New(t)

	// do not expect any error
	a.Nil(err)

	// begin looking at groups
	a.Equal(1, len(syncGroups))
	group := syncGroups["developers"]
	a.Equal(2, len(group.Users))
}

func TestOpenShiftSingleGroupYAML(t *testing.T) {
	syncGroups, err := GetOpenShiftGroupsFromReader(SyncConfig{}, loadTestGroupFile("input-group-single.yml", t))
	testOpenShiftSingleGroup(syncGroups, err, t)
}

func TestOpenShiftSingleGroupJSON(t *testing.T) {
	syncGroups, err := GetOpenShiftGroupsFromReader(SyncConfig{}, loadTestGroupFile("input-group-single.json", t))
	testOpenShiftSingleGroup(syncGroups, err, t)
}