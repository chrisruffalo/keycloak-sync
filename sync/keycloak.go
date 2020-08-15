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

	// get groups
	goCloakGroups, err := getGroupsForRealm(client, realm, token.AccessToken)
	if err != nil {
		logoutErr := logoutKeyCloak(client, realm, token)
		if logoutErr != nil {
			logrus.Warnf("realm %s | could not log out: %s", realm.Name, err)
		}
		return syncGroups, err
	}

	// enhance groups
	enhancedGroups := make([]*keycloakEnhancedGroup, 0, len(*goCloakGroups))
	for _, keycloakGroup := range *goCloakGroups {
		enhancedGroups = append(enhancedGroups, &keycloakEnhancedGroup{
			group: keycloakGroup,
			parent: nil,
		})
	}
	enhancedGroupsPtr := &enhancedGroups

	// make a bit of a set out of the groups for the realm if it is set so that we can filter based on group name
	// in other words "these groups" are the only groups we are looking for. the map -> bool is a cheap-ish
	// way to make a "set" (like a HashSet<String> in java) so that we can do "contains" instead of scanning
	// the array during the filtering process.
	theseGroups := make(map[string]bool)
	if len(realm.Groups) > 0 {
		for _, gName := range realm.Groups {
			theseGroups[gName] = true
		}
	}

	// similarly we have a list of blocked groups, generally both blocked and allowed groups shouldn't be used
	// at the same time but the logic is fairly cheap so we can keep them both in there at the same time
	notTheseGroups := make(map[string]bool)
	if len(realm.BlockedGroups) > 0 {
		for _, gName := range realm.BlockedGroups {
			notTheseGroups[gName] = true
		}
	}

	// now we have all the groups in the target realm
	idx := 0
	for ;; {
		// break when index is greater
		if idx >= len(*enhancedGroupsPtr) {
			break
		}

		// get then increment index
		keyCloakGroup := (*enhancedGroupsPtr)[idx]
		idx++

		if keyCloakGroup == nil || keyCloakGroup.group == nil || keyCloakGroup.group.Name == nil {
			continue
		}
		// if there are values in the group filter we are searching for only "these groups" and so we are skipping
		// any of the groups not found in "these groups"
		if len(theseGroups) > 0 {
			if _, found := theseGroups[*keyCloakGroup.group.Name]; !found {
				continue
			}
		}
		// if the group name is in the set of blocked groups then reject/skip
		if len(notTheseGroups) > 0 {
			if _, found := notTheseGroups[*keyCloakGroup.group.Name]; found {
				continue
			}
		}
		group := Group{
			Id: *keyCloakGroup.group.ID,
			Changed: true, // groups from keycloak are always "changed" because it only matters if an openshift group is changed
			Name: *keyCloakGroup.group.Name,
			Prefix: realm.GroupPrefix,
			Suffix: realm.GroupSuffix,
			SubgroupConcat: realm.SubgroupConcat,
			SubgroupSeparator: realm.SubgroupSeparator,
			Source: "realm:" + realm.Name,
			Realms: []string{realm.Name},
			Users: make(map[string]User),
			Parent: keyCloakGroup.parent,
		}
		// check for an alias and if it exists use it
		if alias, found := realm.Aliases[group.Name]; found {
			group.Alias = alias
		}
		// add found group to map
		syncGroups[group.FinalName()] = group

		// if configured: add subgroups to the list of groups to process
		if realm.Subgroups && keyCloakGroup.group.SubGroups != nil && len(*keyCloakGroup.group.SubGroups) > 0 {
			for _, subgroup := range *keyCloakGroup.group.SubGroups {
				*enhancedGroupsPtr = append(*enhancedGroupsPtr, &keycloakEnhancedGroup{
					group:  &subgroup,
					parent: &group,
				})
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
