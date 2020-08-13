package sync

import "strings"

type SyncGroup struct {
	// properties from external source
	Id          string
	Name     	string
	Alias       string
	Users       map[string]SyncUser

	// properties from configuration
	Prefix		string
	Suffix      string

	// properties that tie the group to where it came from
	Source      string
	// and a list of realms where it came from (if any)
	Realms      []string

	// updated when a meaningful change is made to the
	// group. this is used to provide filtering when
	// the group is sourced from openshift and is either
	// pruned to empty or is changed
	Changed     bool
}

/*
 * FinalName encapsulates the name calculation logic for the SyncGroup
 */
func (sg SyncGroup) FinalName() string {
	if len(sg.Alias) > 0 {
		return sg.Alias
	}
	var builder strings.Builder
	if len(sg.Prefix) > 0 {
		builder.WriteString(sg.Prefix)
	}
	builder.WriteString(sg.Name)
	if len(sg.Suffix) > 0 {
		builder.WriteString(sg.Suffix)
	}
	return builder.String()
}

/*
 * TrimPrunedUsers removes any user that is marked to be pruned
 *                 and then sets the group to changed
 */
func (sg *SyncGroup) TrimPrunedUsers() {
	for key, value := range sg.Users {
		if value.Prune {
			delete(sg.Users, key)
			sg.Changed = true
		}
	}
}

type SyncUser struct {
	Id          string
	Name		string
	Prune       bool
}