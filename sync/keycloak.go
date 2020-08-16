package sync

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/Nerzal/gocloak/v7"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

/*
 * A keycloakEnhancedGroup is an enhancement for a gocloak.Group that
 * allows keeping tabs of which group is the parent and resolving that
 * during processing as well as pushing up users to parent groups to
 * ensure that hierarchical membership is honored
 */
type keycloakEnhancedGroup struct {
	group  *gocloak.Group
	parent *Group
}

func loginKeyCloak(client gocloak.GoCloak, realm RealmConfig) (*gocloak.JWT, error) {
	ctx := context.Background()

	clientConfig := realm.ClientConfig
	userConfig := realm.UserConfig

	var token *gocloak.JWT
	var err error

	if clientConfig != nil {
		token, err = client.LoginClient(ctx, clientConfig.ClientId, clientConfig.ClientSecret, realm.Name)
		if err != nil {
			return token, err
		}

		rptResult, err := client.RetrospectToken(ctx, token.AccessToken, clientConfig.ClientId, clientConfig.ClientSecret, realm.Name)
		if err != nil {
			return token, err
		}
		if rptResult.Active == nil || !(*rptResult.Active) {
			return token, errors.New("inactive token")
		}
	} else if userConfig != nil {
		// determine where to login
		loginRealm := userConfig.LoginRealm
		if len(loginRealm) < 1 {
			loginRealm = realm.Name
		}
		token, err = client.LoginAdmin(ctx, userConfig.Username, userConfig.Password, loginRealm)
	} else {
		err = fmt.Errorf("no client or user configuration provided")
	}
	if err != nil {
		return nil, err
	}
	return token, nil
}

func logoutKeyCloak(client gocloak.GoCloak, realm RealmConfig, token *gocloak.JWT) error {
	var err error

	clientConfig := realm.ClientConfig
	userConfig := realm.UserConfig

	if clientConfig != nil {
		err = client.Logout(context.Background(), clientConfig.ClientId, clientConfig.ClientSecret, realm.Name, token.RefreshToken)
	} else if userConfig != nil {
		// determine where to logout
		loginRealm := userConfig.LoginRealm
		if len(loginRealm) < 1 {
			loginRealm = realm.Name
		}
		err = client.LogoutUserSession(context.Background(), token.AccessToken, loginRealm, token.SessionState)
	} else {
		err = fmt.Errorf("no client or user configuration provided")
	}

	return err
}

/**
 * getGroupsByName returns a list of all the groups (and their subgroups) that match the given "groupName" value. Because
 *                 the keycloak api returns the _root_ group given for a subgroup name this needs to walk up the tree and
 *                 then collect and return relevant subgroups or the "by name" will only work for groups at the root level
 */
func getGroupsByName(client gocloak.GoCloak, realm RealmConfig, accessToken string, groupName string) (*[]*gocloak.Group, error){
	// get all groups and users for each group
	groups, err := client.GetGroups(context.Background(), accessToken, realm.Name, gocloak.GetGroupsParams{
		Search: &groupName,
	})

	if err != nil {
		return nil, err
	}
	if groups == nil {
		return nil, fmt.Errorf("a nil response not expected for groups from realm %s", realm.Name)
	}

	// output groups collects output structure
	var outputGroups []*gocloak.Group

	// if no groups found just return empty list
	if len(groups) < 1 {
		return &groups, nil
	}

	// go through groups and collect up subgroups as well
	idx := 0
	for ;; {
		if idx >= len(groups) {
			break
		}

		group := groups[idx]
		idx++

		if group == nil {
			continue
		}

		// collect the group
		if group.Name != nil && (*group).Name != nil && *group.Name == groupName {
			outputGroups = append(outputGroups, group)
		}

		// go through the subgroups even if realm.Subgroups is not configured because this allows us
		// to find groups at the subgroup level _by name_, having realm.Subgroups as false will be used
		// in the main reconcile loop to prevent having to walk through the groups and fill them all out
		if group.SubGroups != nil && len(*group.SubGroups) > 0 {
			for _, subGroup := range *group.SubGroups {
				groups = append(groups, &subGroup)
			}
		}
	}

	return &outputGroups, nil
}


func getGroupsForRealm(client gocloak.GoCloak, realm RealmConfig, accessToken string) (*[]*gocloak.Group, error){
	// get all groups and users for each group
	groups, err := client.GetGroups(context.Background(), accessToken, realm.Name, gocloak.GetGroupsParams{})
	if err != nil {
		return nil, err
	}
	if groups == nil {
		return nil, fmt.Errorf("a nil response not expected for groups from realm %s", realm.Name)
	}

	return &groups, nil
}

func getUsersForGroup(client gocloak.GoCloak, realm RealmConfig, group Group, accessToken string) ([]*gocloak.User, error) {
	truePtr := true
	falsePtr := false
	usersInGroup, err := client.GetGroupMembers(context.Background(), accessToken, realm.Name, group.Id, gocloak.GetGroupsParams{
		Full: &truePtr,
		BriefRepresentation: &falsePtr,
	})
	if err != nil {
		return nil, err
	}
	if usersInGroup == nil {
		return nil, fmt.Errorf("a nil response not expected for users in group %s from realm %s", group.Name, realm.Name)
	}
	return usersInGroup, nil
}

func getGroupsAndUsersForRealm(realm RealmConfig) (map[string]Group, error) {
	syncGroups := make(map[string]Group)

	// create client for realm
	client := gocloak.NewClient(realm.Url)
	restyClient := client.RestyClient()
	if viper.GetBool("keycloak-debug") {
		restyClient.SetDebug(true)
	}
	if !realm.SslVerify {
		restyClient.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})
	}

	// login with client and get token
	token, err := loginKeyCloak(client, realm)
	if err != nil {
		if token != nil && len(token.RefreshToken) > 0 {
			logoutErr := logoutKeyCloak(client, realm, token)
			if logoutErr != nil {
				logrus.Warnf("realm %s | could not log out: %s", realm.Name, err)
			}
		}
		return syncGroups, err
	}

	// group array there are two different sources for this (all groups or groups by id)
	var goCloakGroups *[]*gocloak.Group

	// get a list of all the groups from the realm.
	if len(realm.Groups) > 0 {
		gcg := make([]*gocloak.Group, 0, len(realm.Groups))
		for _, groupName := range realm.Groups {
			if len(groupName) < 1 {
				continue
			}
			// get groups by name from keycloak
			groups, err := getGroupsByName(client, realm, token.AccessToken, groupName)
			if err != nil {
				logrus.Warnf("realm %s | could not get group named %s", realm.Name, groupName)
				continue
			}
			// for the list of found groups go through them and add them to the list
			for _, foundGroup := range *groups {
				if foundGroup == nil {
					continue
				}
				gcg = append(gcg, foundGroup)
			}
			// assign to list
			goCloakGroups = &gcg
		}
	} else {
		goCloakGroups, err = getGroupsForRealm(client, realm, token.AccessToken)
		if err != nil {
			logoutErr := logoutKeyCloak(client, realm, token)
			if logoutErr != nil {
				logrus.Warnf("realm %s | could not log out: %s", realm.Name, err)
			}
			return syncGroups, err
		}
	}

	// enhance groups
	enhancedGroups := make([]*keycloakEnhancedGroup, 0, len(*goCloakGroups))
	for _, keycloakGroup := range *goCloakGroups {
		enhancedGroups = append(enhancedGroups, &keycloakEnhancedGroup{
			group: keycloakGroup,
			parent: nil,
		})
	}

	// this serves as a "set" of blocked groups to allow the blocking of individual groups on a case-by case basis
	// which would be used block individual groups by their keycloak name which can be used when getting all groups
	// or when blocking subgroups if using the realm.Groups configuration option.
	notTheseGroups := make(map[string]bool)
	if len(realm.BlockedGroups) > 0 {
		for _, gName := range realm.BlockedGroups {
			notTheseGroups[gName] = true
		}
	}

	// similarly to blocked groups this gives us the ability to block names on fully resolved groups this can be used
	// if there is a situation where the group tree has names that are the same but the calculated final name is different
	// and one of them needs to be remove
	notTheseNames := make(map[string]bool)
	if len(realm.BlockedNames) > 0 {
		for _, bName := range realm.BlockedNames {
			notTheseNames[bName] = true
		}
	}

	// cycle through the groups in a way that allows groups to be added so that subgroups can be added and resolved
	// without recursion
	idx := 0
	for ;; {
		// break when index is greater
		if idx >= len(enhancedGroups) {
			break
		}

		// get then increment index
		keyCloakGroup := enhancedGroups[idx]
		idx++

		// skip invalid/unusable groups
		if keyCloakGroup == nil || keyCloakGroup.group == nil || keyCloakGroup.group.Name == nil {
			continue
		}

		// used to determine if this group was skipped as a result of some filtering action
		skipped := false

		// if the group name is in the set of blocked groups then reject/skip
		if len(notTheseGroups) > 0 {
			if _, found := notTheseGroups[*keyCloakGroup.group.Name]; found {
				skipped = true
			}
		}

		// create group
		group := Group{
			Id: *keyCloakGroup.group.ID,
			Changed: true, // groups from keycloak are always "changed" because it only matters if an openshift group is changed
			Name: *keyCloakGroup.group.Name,
			Path: *keyCloakGroup.group.Path,
			Prefix: realm.GroupPrefix,
			Suffix: realm.GroupSuffix,
			SubgroupConcat: realm.SubgroupConcat,
			SubgroupSeparator: realm.SubgroupSeparator,
			Source: "realm:" + realm.Name,
			Realms: []string{realm.Name},
			Users: make(map[string]User),
			Parent: keyCloakGroup.parent,
			Skipped: skipped,
		}

		// check for an alias and if it exists use it
		if alias, found := realm.Aliases[group.Name]; found {
			group.Alias = alias
		}

		// if configured: add subgroups to the list of groups to process
		if realm.Subgroups && keyCloakGroup.group.SubGroups != nil && len(*keyCloakGroup.group.SubGroups) > 0 {
			for _, subgroup := range *keyCloakGroup.group.SubGroups {
				enhancedGroups = append(enhancedGroups, &keycloakEnhancedGroup{
					group:  &subgroup,
					parent: &group,
				})
			}
		}

		// add found group to map if it shouldn't be skipped
		if !skipped {
			finalName := group.FinalName()

			// ensure that the final name does not exist in the list of blocked final
			// names before adding to the map of returned groups
			if _, found := notTheseNames[finalName]; !found {
				syncGroups[finalName] = group
			}
		}
	}

	// establish the users that belong to the group
	for _, group := range syncGroups {
		usersInGroup, err := getUsersForGroup(client, realm, group, token.AccessToken)
		if err != nil {
			logrus.Errorf("%s", err)
			continue
		}
		for _, userInGroup := range usersInGroup {
			// skip null user/usernames, we could log here but this really shouldn't happen
			if userInGroup == nil || userInGroup.Username == nil {
				continue
			}
			// add user to group map
			group.Users[*userInGroup.Username] = User{
				Id: *userInGroup.ID,
				Name: *userInGroup.Username,
			}
			if realm.SubgroupUsers {
				// recursively add user to all parent groups
				parentGroup := group.Parent
				for ; parentGroup != nil; {
					if _, found := parentGroup.Users[*userInGroup.Username]; !found {
						parentGroup.Users[*userInGroup.Username] = User{
							Id:   *userInGroup.ID,
							Name: *userInGroup.Username,
						}
					}
					parentGroup = parentGroup.Parent
				}
			}
		}
	}

	err = logoutKeyCloak(client, realm, token)
	if err != nil {
		logrus.Warnf("realm %s | could not log out: %s", realm.Name, err)
	}

	return syncGroups, nil
}

func GetKeycloakGroupsFromRealm(realm RealmConfig) (GroupList, error) {
	groupsForRealm, err := getGroupsAndUsersForRealm(realm)
	if err != nil {
		return groupsForRealm, err
	}
	if len(groupsForRealm) < 1 {
		return groupsForRealm, errors.New("no groups returned for realm")
	}
	return groupsForRealm, nil
}

func GetKeycloakGroups(syncConfig Config) map[string]Group {
	groupList := GroupList{}
	for _, realm := range syncConfig.Realms {
		groupsForRealm, err := GetKeycloakGroupsFromRealm(realm)
		if err != nil {
			logrus.Errorf("realm %s | %s", realm.Name, err)
			continue
		}
		groupList = Merge(groupList, groupsForRealm)
	}
	return groupList
}
