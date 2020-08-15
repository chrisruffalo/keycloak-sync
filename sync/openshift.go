package sync

import (
	"bytes"
	userapi "github.com/openshift/api/user/v1"
	"io"
	"k8s.io/apimachinery/pkg/util/yaml"
)

/**
 * GetOpenShiftGroupsFromReader gets the openshift groups by parsing the data in the reader and bending the
 *                              given input model to the Group struct.
 */
func GetOpenShiftGroupsFromReader(config Config, reader io.Reader) (GroupList, error) {
	output := GroupList{}

	var buf bytes.Buffer
	doubleReader := io.TeeReader(reader, &buf)

	// read yaml or json
	decoder := yaml.NewYAMLOrJSONDecoder(doubleReader, 4096)
	ocGroups := userapi.GroupList{}
	err := decoder.Decode(&ocGroups)
	if err != nil || ocGroups.TypeMeta.Kind == "Group" {
		// if the group is not read try and read a single item
		decoder := yaml.NewYAMLOrJSONDecoder(&buf, 4096)
		singleGroup := userapi.Group{}
		err := decoder.Decode(&singleGroup)
		if err == nil {
			ocGroups = userapi.GroupList{}
			ocGroups.Items = []userapi.Group{
				singleGroup,
			}
		} else {
			return output, err
		}
	}

	// no error but also no groups
	if len(ocGroups.Items) < 1 {
		return output, nil
	}

	for _, item := range ocGroups.Items {
		syncGroup := Group{
			Id: "openshift",
			Source: "openshift",
			Name: item.Name,
			Users: make(map[string]User),
		}
		// this is the same as "Name" but maintains consistency
		output[syncGroup.FinalName()] = syncGroup

		// continue to users phase
		if item.Users == nil || len(item.Users) < 0 {
			continue
		}
		// add users to group if there are users
		for _, user := range item.Users {
			syncUser := User{
				Id: "openshift",
				Name: user,
				Prune: config.Prune, // uses the config setting so these can be trimmed later
			}
			syncGroup.Users[syncUser.Name] = syncUser
		}
	}

	return output, nil
}
