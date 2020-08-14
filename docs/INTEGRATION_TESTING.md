# Integration Testing
Create a stand-alone Keycloak and OpenLdap server and integrate them for testing. This configuration is persistent so
once the initial configuration is created you can just restart the pod and the containers.

## Create Environment
```bash
[host]$ mkdir -p ~/containers/data/{keycloak-pg/pgdata,ldif}
[host]$ cat <<EOF > ~/containers/data/ldif/50-custom.ldif
dn: ou=groups,dc=example,dc=org
ou: groups
description: list of groups
objectclass: organizationalunit

dn: cn=admins,ou=groups,dc=example,dc=org
cn: admins
member: uid=rjsmith,ou=people,dc=example,dc=org
description: Users who can administer anything
objectclass: top
objectclass: groupOfNames

dn: cn=developers,ou=groups,dc=example,dc=org
cn: developers
member: uid=jdoe,ou=people,dc=example,dc=org
description: Users who can develop things
objectclass: top
objectclass: groupOfNames

dn: ou=people,dc=example,dc=org
ou: people
description: All people in organisation
objectclass: organizationalunit

dn: cn=Robert Smith,ou=people,dc=example,dc=org
objectclass: inetOrgPerson
cn: Robert J Smith
sn: smith
uid: rjsmith
mail: r.smith@example.com
description: test user

dn: cn=Jane Doe,ou=people,dc=example,dc=org
objectclass: inetOrgPerson
cn: Jane Doe
sn: jdoe
uid: jdoe
mail: j.doe@example.com
description: test user
EOF
```

## Start Containers in Pod
```bash
[host]$ podman pod create --name kc -p 8080:8080
[host]$ podman run -d --name postgres --pod kc --volume ~/containers/data/keycloak-pg:/var/lib/postgresql/data -e PGDATA=/var/lib/postgresql/data/pgdata:z -e POSTGRES_DB=keycloak -e POSTGRES_USER=keycloak -e POSTGRES_PASSWORD=password postgres
[host]$ podman run -d --name ldap --pod kc --volume ~/containers/data/ldif:/container/service/slapd/assets/config/bootstrap/ldif/custom:z osixia/openldap:latest --copy-service
[host]$ podman run -d --name keycloak --pod kc -e DB_VENDOR=postgres -e DB_ADDR=localhost -e DB_USER=keycloak -e DB_PASSWORD=password -e KEYCLOAK_USER=admin -e KEYCLOAK_PASSWORD=admin quay.io/keycloak/keycloak:11.0.0
```

### Stop Pod
```bash
[host]$ podman pod rm -f kc
```

## Keycloak Setup

Then set up realm and attach to ldap (user federation):
```
Vendor: Other
Username LDAP Attribute: uid
RDN LDAP attribute: uid
UUID LDAP attribute: uid
User Object Classes: inetOrgPerson
Connection URL: ldap://localhost:389
Users DN: ou=people,dc=example,dc=org
Bind DN: cn=admin,dc=example,dc=org
Bind Credential: admin
```

Add group mapper (mappers > create):
```
Name: ldap-group-mapper
Mapper Type: group-ldap-mapper
LDAP Groups DN: ou=groups,dc=example,dc=org
Group Name LDAP Attribute: cn
Group Object Classes: groupofNames
Membership LDAP Attribute: member
Membership Attribute Type: DN
Membership User LDAP Attribute: uid
Mode: READ_ONLY
User Groups Retrieve Strategy: LOAD_GROUPS_BY_MEMBER_ATTRIBUTE
```

Refer to client setup to set up the openid-connect client.