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

func loginKeyCloak(client gocloak.GoCloak, realm RealmConfig) (*gocloak.JWT, error) {
	ctx := context.Background()
	token, err := client.LoginClient(ctx, realm.ClientId, realm.ClientSecret, realm.Name)
	if err != nil {
		return nil, err
	}
	rptResult, err := client.RetrospectToken(ctx, token.AccessToken, realm.ClientId, realm.ClientSecret, realm.Name)
	if err != nil {
		return token, err
	}
	if rptResult.Active == nil || !(*rptResult.Active) {
		return token, errors.New("inactive token")
	}
	return token, nil
}

func logoutKeyCloak(client gocloak.GoCloak, realm RealmConfig, refreshToken string) error {
	return client.Logout(context.Background(), realm.ClientId, realm.ClientSecret, realm.Name, refreshToken)
}

func getGroupsForRealm(client gocloak.GoCloak, realm RealmConfig, accessToken string) ([]*gocloak.Group, error){
	// get all groups and users for each group
	groups, err := client.GetGroups(context.Background(), accessToken, realm.Name, gocloak.GetGroupsParams{})
	if err != nil {
		return nil, err
	}
	if groups == nil {
		return nil, fmt.Errorf("a nil response not expected for groups from realm %s", realm.Name)
	}

	return groups, nil
}

func getUsersForGroup(client gocloak.GoCloak, realm RealmConfig, group SyncGroup, accessToken string) ([]*gocloak.User, error) {
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

func getGroupsAndUsersForRealm(realm RealmConfig) (map[string]SyncGroup, error) {
	syncGroups := make(map[string]SyncGroup)

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
			logoutErr := logoutKeyCloak(client, realm, token.RefreshToken)
			if logoutErr != nil {
				logrus.Warnf("realm %s | could not log out: %s", realm.Name, err)
			}
		}
		return syncGroups, err
	}

	// get groups
	goCloakGroups, err := getGroupsForRealm(client, realm, token.AccessToken)
	if err != nil {
		logoutErr := logoutKeyCloak(client, realm, token.RefreshToken)
		if logoutErr != nil {
			logrus.Warnf("realm %s | could not log out: %s", realm.Name, err)
		}
		return syncGroups, err
	}

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

	// now we have all the groups in the target realm
	for _, keyCloakGroup := range goCloakGroups {
		if keyCloakGroup == nil || keyCloakGroup.Name == nil {
			continue
		}
		// if there are values in the group filter we are searching for only "these groups" and so we are skipping
		// any of the groups not found in "these groups"
		if len(theseGroups) > 0 {
			if _, found := theseGroups[*keyCloakGroup.Name]; !found {
				continue
			}
		}
		group := SyncGroup{
			Id: *keyCloakGroup.ID,
			Changed: true, // groups from keycloak are always "changed" because it only matters if an openshift group is changed
			Name: *keyCloakGroup.Name,
			Prefix: realm.GroupPrefix,
			Suffix: realm.GroupSuffix,
			Source: "realm:" + realm.Name,
			Users: make(map[string]SyncUser),
		}
		// check for an alias and if it exists use it
		if alias, found := realm.Aliases[group.Name]; found {
			group.Alias = alias
		}
		// add found group to map
		syncGroups[group.Name] = group
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
			group.Users[*userInGroup.Username] = SyncUser{
				Id: *userInGroup.ID,
				Name: *userInGroup.Username,
			}
		}
	}

	err = logoutKeyCloak(client, realm, token.RefreshToken)
	if err != nil {
		logrus.Warnf("realm %s | could not log out: %s", realm.Name, err)
	}

	return syncGroups, nil
}

/**
 * merge returns a map of group names to group with all the users merged together. if the sync config specifies
 *       that a merge is _not_ to be done then any groups that have the same name are dropped. the merging is
 *       performed (or dropped) _after_ aliases and prefix/suffix are applied
 */
func merge(config SyncConfig, realmToGroupsMap map[string]map[string]SyncGroup, mergeInto map[string]SyncGroup) map[string]SyncGroup {
	// go based on the order of realms so that the first realm gets priority and subsequent names are dropped in the
	// event of merge being false
	for _, realmConfig := range config.Realms {
		groupsForRealm, found := realmToGroupsMap[realmConfig.Name]
		// if no groups for given realm name, skip
		if !found {
			continue
		}
		// cycle through groups in realm and put/merge as needed
		for _, group := range groupsForRealm {
			// determine if group is already in map
			alreadyGroup, alreadyInMap := mergeInto[group.FinalName()]
			if alreadyInMap {
				// update realms
				alreadyGroup.Realms = append(alreadyGroup.Realms, realmConfig.Name)
				mergeInto[alreadyGroup.FinalName()] = alreadyGroup

				// if we are not configured for merge: warn and continue (drop)
				if !config.Merge {
					logrus.Warnf("the group %s already exists from a previous realm, dropping %s's %s group", group.FinalName(), realmConfig.Name, group.FinalName())
					continue
				}
				// proceed with merge behavior
				for _, user := range group.Users {
					// if the user is already in the group warn during merge
					if _, userAlreadyInGroup := alreadyGroup.Users[user.Name]; userAlreadyInGroup {
						// update user in map
						doNotPruneUser := mergeInto[alreadyGroup.FinalName()].Users[user.Name]
						doNotPruneUser.Prune = false
						mergeInto[alreadyGroup.FinalName()].Users[user.Name] = doNotPruneUser

						// this is supposed to happen for values from openshift, suppress warning
						if alreadyGroup.Source != "openshift" {
							logrus.Warnf("User %s already found in group %s", user.Name, group.FinalName())
						}
						continue
					}
					// set the user as not to be pruned
					// add user to group
					alreadyGroup.Users[user.Name] = user
					// group should be marked as changed (which only applies to openshift-sourced groups)
					alreadyGroup.Changed = true
					// gets around a weird issue with the reference to alreadyGroup and changing the value of Changed
					mergeInto[alreadyGroup.FinalName()] = alreadyGroup
				}
			} else {
				// not already in map so put it there
				mergeInto[group.FinalName()] = group
			}
		}
	}

	return mergeInto
}

func GetKeycloakGroups(syncConfig SyncConfig, mergeInto map[string]SyncGroup) map[string]SyncGroup {
	// this maps each realm to a list of groups, this will be fixed during the "merge"
	// step to create a canonical list of groups regardless of realm
	realmToGroupsMap := make(map[string]map[string]SyncGroup)
	for _, realm := range syncConfig.Realms {
		groupsForRealm, err := getGroupsAndUsersForRealm(realm)
		if err != nil {
			logrus.Errorf("realm: %s | %s", realm.Name, err)
			continue
		}
		if len(groupsForRealm) < 1 {
			logrus.Errorf("realm %s | no groups returned for realm", realm.Name)
			continue
		}
		realmToGroupsMap[realm.Name] = groupsForRealm
	}

	// get the final list of groups
	return merge(syncConfig, realmToGroupsMap, mergeInto)
}
