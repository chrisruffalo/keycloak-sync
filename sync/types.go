package sync

import (
	userapi "github.com/openshift/api/user/v1"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"keycloak-sync/constants"
	"strings"
)

/**
 * Group is a map that adds functionality for converting a collection of
 *           groups to/from OpenShift groups
 */
type GroupList map[string]Group

func FromOpenShiftGroups(config SyncConfig, groupList userapi.GroupList) GroupList {
	groups := &GroupList{}

	// for each item create a group item
	for _, item := range groupList.Items {
		group := FromOpenShiftGroup(config, item)
		(*groups)[group.FinalName()] = group
	}

	return *groups
}

/*
 * Merge returns a GroupList that is the product of copying the targetGroup and then
 *       merging the sourceGroup on top of it. Neither the targetGroup or the sourceGroup
 *       are changed during this operation.
 */
func Merge(targetGroup GroupList, sourceGroup GroupList) GroupList {
	// start from the output group
	outputGroup := targetGroup.copy()

	// cycle through groups in realm and put/merge as needed
	for _, group := range sourceGroup {
		// determine if group is already in map
		alreadyGroup, alreadyInMap := outputGroup[group.FinalName()]
		if alreadyInMap {
			// update realms
			alreadyGroup.Realms = append(alreadyGroup.Realms, group.Realms...)
			outputGroup[alreadyGroup.FinalName()] = alreadyGroup

			// proceed with merge behavior
			for _, user := range group.Users {
				// if the user is already in the group warn during merge
				if _, userAlreadyInGroup := alreadyGroup.Users[user.Name]; userAlreadyInGroup {
					// update user in map
					doNotPruneUser := outputGroup[alreadyGroup.FinalName()].Users[user.Name]
					doNotPruneUser.Prune = false
					outputGroup[alreadyGroup.FinalName()].Users[user.Name] = doNotPruneUser

					logrus.Warnf("User %s already found in group %s", user.Name, group.FinalName())
					continue
				}
				// set the user as not to be pruned
				// add user to group
				alreadyGroup.Users[user.Name] = user
				// group should be marked as changed (which only applies to openshift-sourced groups)
				alreadyGroup.Changed = true
				// gets around a weird issue with the reference to alreadyGroup and changing the value of Changed
				outputGroup[alreadyGroup.FinalName()] = alreadyGroup
			}
		} else {
			// not already in map so put it there
			outputGroup[group.FinalName()] = group
		}
	}

	return outputGroup
}


func (sgs *GroupList) ToOpenShiftGroups(config SyncConfig, onlyChanged bool) userapi.GroupList {
	// create group shell
	groups := &userapi.GroupList{
		TypeMeta: v1.TypeMeta{
			Kind:       "GroupList",
			APIVersion: userapi.GroupVersion.Version,
		},
		ListMeta: v1.ListMeta{},
		Items:    make([]userapi.Group, 0, len(*sgs)),
	}

	// convert the group map into openshift groups
	for _, group := range *sgs {
		openshiftGroup, changed := group.ToOpenShiftGroup(config)

		// if we are only looking for changed group and the group
		// has not changed then we want to skip it
		if onlyChanged && !changed {
			continue
		}

		// create openshift group and append to list
		groups.Items = append(groups.Items, openshiftGroup)
	}

	// return groups
	return *groups
}

func (sgs GroupList) copy() GroupList {
	output := GroupList{}
	for _, item := range sgs {
		group := item.copy()
		output[group.FinalName()] = group
	}
	return output
}

type Group struct {
	// properties from external source
	Id          string
	Name     	string
	Alias       string
	Users       map[string]User

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

func FromOpenShiftGroup(config SyncConfig, group userapi.Group) Group {
	userMap := make(map[string]User)

	// add users to user map
	for _, user := range group.Users {
		userMap[user] = User{
			Id:    user,
			Name:  user,
			Prune: config.Prune,
		}
	}

	syncGroup := Group{
		Id:      group.Name,
		Name:    group.Name,
		Alias:   "",
		Users:   userMap,
		Prefix:  "",
		Suffix:  "",
		Source:  "openshift",
		Realms:  []string{},
		Changed: false,
	}

	return syncGroup
}

/*
 * FinalName encapsulates the name calculation logic for the Group
 */
func (sg Group) FinalName() string {
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

func (sg *Group) ToOpenShiftGroup(config SyncConfig) (userapi.Group, bool) {
	// list of users
	users := userapi.OptionalNames{}

	// if a changing action happens (prune, etc) then the value is changed
	changed := false

	for _, user := range sg.Users {
		// skip users that are marked for prune if the prune feature is configured
		if config.Prune && user.Prune {
			// changed when a user is pruned
			changed = true
			continue
		}
		// add users to the list
		users = append(users, user.Name)
	}

	// create openshift group
	openshiftGroup := &userapi.Group{
		TypeMeta:   v1.TypeMeta{
			Kind:       "Group",
			APIVersion: userapi.GroupVersion.Version,
		},
		ObjectMeta: v1.ObjectMeta{
			Name: sg.Name,
		},
		Users:      users,
	}

	// add annotations
	openshiftGroup.SetAnnotations(map[string]string{
		constants.AnnotationCreatedBy: "keycloak-sync",
		constants.AnnotationPrimarySource: sg.Source,
		constants.AnnotationRealms: strings.Join(sg.Realms, ","),
	})

	// return the group and the status on if it was changed or not
	// meaning that it was either changed by another step or the users
	// were pruned here
	return *openshiftGroup, sg.Changed || changed
}

/*
 * TrimPrunedUsers removes any user that is marked to be pruned
 *                 and then sets the group to changed
 */
func (sg *Group) TrimPrunedUsers() {
	for key, value := range sg.Users {
		if value.Prune {
			delete(sg.Users, key)
			sg.Changed = true
		}
	}
}

func (sg Group) copy() Group {
	users := make(map[string]User)

	// copy users
	for _, u := range sg.Users {
		user := u.copy()
		users[user.Name] = user
	}

	// copy realms
	realms := make([]string, 0, len(sg.Realms))
	copy(realms, sg.Realms)

	return Group{
		Id:      sg.Id,
		Name:    sg.Name,
		Alias:   sg.Alias,
		Users:   users,
		Prefix:  sg.Prefix,
		Suffix:  sg.Suffix,
		Source:  sg.Source,
		Realms:  realms,
		Changed: sg.Changed,
	}
}

/*
 * Group represents a single user
 */
type User struct {
	Id          string
	Name		string
	Prune       bool
}

func (u User) copy() User {
	return User{
		Id:    u.Id,
		Name:  u.Name,
		Prune: u.Prune,
	}
}