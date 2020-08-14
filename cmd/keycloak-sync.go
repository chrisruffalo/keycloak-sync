package main

import (
	"bytes"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"io"
	"k8s.io/apimachinery/pkg/util/json"
	"keycloak-sync/sync"
	"os"
	"strings"
)

const (
	_EXIT_OK              = 0
	// configuration issues
	_ERROR_NO_CONFIG 	  = 100
	_ERROR_CONFIG_MISSING = 101
	_ERROR_READING_CONFIG = 102
)

/*
 * Entrypoint for keycloak-sync command.
 */
func main() {

	// read command line options
	pflag.StringP("config", "c", "keycloak-sync.yml", "The path to the config file that drives the configuration. A config file is required.")
	pflag.StringP("groups", "g", "", "The path to the OpenShift group list file (yaml or json) that should be used to reconcile the groups from Keycloak/SSO. Use \"-\" to provide on stdin.")
	pflag.BoolP("keycloak-debug", "D", false, "Debug the rest input/output of the keycloak exchange.")
	pflag.BoolP("help", "h", false, "Print the help message")
	pflag.Parse()
	err := viper.BindPFlags(pflag.CommandLine)
	if err != nil {
		logrus.Errorf("Could not bind flags: %s", err)
		os.Exit(1)
	}

	// show help if asked
	if viper.GetBool("help") {
		fmt.Print("keycloak-sync\n")
		pflag.PrintDefaults()
		os.Exit(_EXIT_OK)
	}

	// ensure configuration exists
	configFile := viper.GetString("config")
	if len(strings.TrimSpace(configFile)) < 1 {
		logrus.Error("A configuration file is required")
		os.Exit(_ERROR_NO_CONFIG)
	}
	_, fileErr := os.Stat(configFile)
	if os.IsNotExist(fileErr) {
		logrus.Errorf("The configuration file %s does not exist", configFile)
		os.Exit(_ERROR_CONFIG_MISSING)
	}
	viper.AddConfigPath(".")
	config, err := sync.LoadConfig(configFile)
	if err != nil {
		logrus.Errorf("Could not read config file: %s", err)
		os.Exit(_ERROR_READING_CONFIG)
	}

	if len(config.Realms) < 1 {
		logrus.Error("No realms provided in configuration")
		os.Exit(1)
	}

	// if we want to track just changed groups this brings in groups from openshift for that
	onlyChanged := false

	// if openshift groups are provided, read them by figuring out the reader
	openshiftGroups := sync.GroupList{}
	groupsFileName := strings.TrimSpace(viper.GetString("groups"))
	if len(groupsFileName) > 0 {
		var reader io.Reader
		if groupsFileName == "-" {
			reader = os.Stdin
		} else if _, fileErr := os.Stat(groupsFileName); !os.IsNotExist(fileErr) {
			reader, err = os.Open(groupsFileName)
			if err != nil {
				logrus.Errorf("Could not open OpenShift groups file: %s", err)
				os.Exit(1)
			}
		} else {
			logrus.Errorf("No file named '%s' fround as source for OpenShift groups")
			os.Exit(1)
		}
		openshiftGroups, err = sync.GetOpenShiftGroupsFromReader(config, reader)
		if err != nil {
			logrus.Errorf("Could not read OpenShift group information from '%s': %s", groupsFileName, err)
			os.Exit(1)
		}
		onlyChanged = true
	}

	// get groups providing the openshift groups as the target for merging on to
	keycloakGroups := sync.GetKeycloakGroups(config)
	finalGroups := sync.Merge(openshiftGroups, keycloakGroups)

	// create openshift groups
	outputGroups := finalGroups.ToOpenShiftGroups(config, onlyChanged)

	// encode to json
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(outputGroups)
	if err != nil {
		logrus.Errorf("Error encoding OpenShift objects: %s", err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(buf.Bytes())

	//buf.Reset()
	//yEncoder := yaml.NewEncoder(&buf)
	//yEncoder.SetIndent(2)
	//yEncoder.Encode(outputGroups)
	//_, _ = os.Stdout.Write(buf.Bytes())

}

