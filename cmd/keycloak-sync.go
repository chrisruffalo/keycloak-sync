package main

import (
	"bytes"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"io"
	"keycloak-sync/sync"
	"os"
	"strings"
	"text/template"
)

const (
	_EXIT_OK              = 0
	// configuration issues
	_ERROR_NO_CONFIG 	  = 100
	_ERROR_CONFIG_MISSING = 101
	_ERROR_READING_CONFIG = 102
)

// simple template for OpenShift/k8s Group objects
const _GROUP_TEMPLATE =
`{{- range $idx, $group := . -}}
---
apiVersion: user.openshift.io/v1
kind: Group
metadata:
  name: {{ $group.FinalName }}
  annotations:
    "keycloak-sync/last-primary-source": "{{ $group.Source }}"
    {{- $length := len $group.Realms -}}{{- if gt $length 0 }}
    "keycloak-sync/realms": "{{ range $index, $realm := $group.Realms }}{{ if $index}}, {{end}}{{ $realm }}{{ end }}"
	{{- end }}
users:
{{- range $user_key, $user := $group.Users }}
- {{ $user.Name }}
{{- end -}}
{{ "\n" }}
{{- end -}}`

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

	// if openshift groups are provided, read them by figuring out the reader
	openshiftGroups := make(map[string]sync.SyncGroup)
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
	}

	if len(config.Realms) < 1 {
		logrus.Error("No realms provided in configuration")
		os.Exit(1)
	}

	// get groups providing the openshift groups as the target for merging on to
	finalGroups := sync.GetKeycloakGroups(config, openshiftGroups)

	// changed groups are the only groups that should be shown in the output, of course if
	// no openshift source is given all the output will have been "changed"
	var changedGroups map[string]sync.SyncGroup
	if len(groupsFileName) > 0 {
		changedGroups = make(map[string]sync.SyncGroup)
		for key, value := range finalGroups {
			// prune before determining change status
			if config.Prune {
				value.TrimPrunedUsers()
			}
			// if changed then add to the list to be output
			if value.Changed {
				changedGroups[key] = value
			}
		}
	} else {
		// if no group file was provided the changed groups are the final groups
		changedGroups = finalGroups
	}

	// create new expected state
	tmpl, err := template.New("groups").Parse(_GROUP_TEMPLATE)
	if err != nil {
		logrus.Errorf("Could not parse template: %s", err)
		os.Exit(1)
	}

	// execute template and trim for output
	var tpl bytes.Buffer
	if err := tmpl.Execute(&tpl, changedGroups); err != nil {
		logrus.Errorf("Could not execute template: %s", err)
		os.Exit(1)
	}
	result := tpl.String()
	fmt.Print(strings.TrimSpace(result))
}