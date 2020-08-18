package sync

import (
	"github.com/chrisruffalo/keycloak-sync/constants"
	userapi "github.com/openshift/api/user/v1"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
)

/**
 * Group is a map that adds functionality for converting a collection of
 *           groups to/from OpenShift groups
 */
type GroupList map[string]Group

func FromOpenShiftGroups(config Config, groupList userapi.GroupList) GroupList {
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

				// todo: add children
				// todo: add... parents?
			}
		} else {
			// not already in map so put it there
			outputGroup[group.FinalName()] = group
		}
	}

	return outputGroup
}

func (sgs *GroupList) ToOpenShiftGroups(config Config, onlyChanged bool) userapi.GroupList {
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
		// don't convert skipped groups
		if group.Skipped {
			continue
		}

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
	Id string

	// naming configuration
	Name              string
	Alias             string
	Prefix            string
	Suffix            string
	SubgroupConcat    bool
	SubgroupSeparator string

	// map of user names -> user
	Users map[string]User

	// hierarchy of groups/subgroups
	Path     string
	Parent   *Group
	Children map[string]Group

	// properties that tie the group to where it came from
	Source string
	// and a list of realms where it came from (if any)
	Realms []string

	// updated when a meaningful change is made to the
	// group. this is used to provide filtering when
	// the group is sourced from openshift and is either
	// pruned to empty or is changed
	Changed bool

	// set to true if this group was previously skipped.
	// if the group was previously skipped that doesn't
	// mean that the children should be
	Skipped bool
}

func FromOpenShiftGroup(config Config, group userapi.Group) Group {
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
		Id:       group.Name,
		Name:     group.Name,
		Alias:    "",
		Prefix:   "",
		Suffix:   "",
		Users:    userMap,
		Parent:   nil,
		Children: map[string]Group{},
		Source:   "openshift",
		Realms:   []string{},
		Changed:  false,
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
	if sg.SubgroupConcat && sg.Parent != nil {
		parentNames := make([]string, 0)
		parent := sg.Parent
		for parent != nil {
			if !parent.Skipped {
				parentNames = append([]string{parent.Name}, parentNames...)
			}
			parent = parent.Parent
		}
		// only continue if more names are found
		if len(parentNames) > 0 {
			nameSeparator := sg.SubgroupSeparator
			if len(strings.TrimSpace(nameSeparator)) < 1 {
				nameSeparator = "."
			}
			builder.WriteString(strings.Join(parentNames, nameSeparator))
			builder.WriteString(nameSeparator)
		}
	}
	builder.WriteString(sg.Name)
	if len(sg.Suffix) > 0 {
		builder.WriteString(sg.Suffix)
	}
	return builder.String()
}

func (sg *Group) ToOpenShiftGroup(config Config) (userapi.Group, bool) {
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
		TypeMeta: v1.TypeMeta{
			Kind:       "Group",
			APIVersion: userapi.GroupVersion.Version,
		},
		ObjectMeta: v1.ObjectMeta{
			Name: sg.FinalName(),
		},
		Users: users,
	}

	// add annotations
	openshiftGroup.SetAnnotations(map[string]string{
		constants.AnnotationCreatedBy:     "keycloak-sync",
		constants.AnnotationPrimarySource: sg.Source,
		constants.AnnotationRealms:        strings.Join(sg.Realms, ","),
	})

	// return the group and the status on if it was changed or not
	// meaning that it was either changed by another step or the users
	// were pruned here
	return *openshiftGroup, sg.Changed || changed
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

	children := make(map[string]Group)
	for _, child := range sg.Children {
		children[child.Name] = child.copy()
	}

	group := Group{
		Id:                sg.Id,
		Name:              sg.Name,
		Alias:             sg.Alias,
		Path:              sg.Path,
		Prefix:            sg.Prefix,
		Suffix:            sg.Suffix,
		SubgroupConcat:    sg.SubgroupConcat,
		SubgroupSeparator: sg.SubgroupSeparator,
		Users:             users,
		Source:            sg.Source,
		Realms:            realms,
		Changed:           sg.Changed,
		Children:          children,
		Skipped:           sg.Skipped,
	}

	// copy parent
	if sg.Parent != nil {
		parent := sg.Parent.copy()
		group.Parent = &parent
	}

	return group
}

/*
 * Group represents a single user
 */
type User struct {
	Id    string
	Name  string
	Prune bool
}

func (u User) copy() User {
	return User{
		Id:    u.Id,
		Name:  u.Name,
		Prune: u.Prune,
	}
}
