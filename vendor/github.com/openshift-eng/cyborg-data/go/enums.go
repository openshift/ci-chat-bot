package orgdatacore

type MembershipType string

const (
	MembershipTeam      MembershipType = "team"
	MembershipOrg       MembershipType = "org"
	MembershipPillar    MembershipType = "pillar"
	MembershipTeamGroup MembershipType = "team_group"
)

func (m MembershipType) String() string { return string(m) }

func (m MembershipType) IsValid() bool {
	switch m {
	case MembershipTeam, MembershipOrg, MembershipPillar, MembershipTeamGroup:
		return true
	}
	return false
}

type OrgInfoType string

const (
	OrgTypeOrganization OrgInfoType = "Organization"
	OrgTypeTeam         OrgInfoType = "Team"
	OrgTypePillar       OrgInfoType = "Pillar"
	OrgTypeTeamGroup    OrgInfoType = "Team Group"
	OrgTypeParentTeam   OrgInfoType = "Parent Team"
)

func (o OrgInfoType) String() string { return string(o) }

func (o OrgInfoType) IsValid() bool {
	switch o {
	case OrgTypeOrganization, OrgTypeTeam, OrgTypePillar, OrgTypeTeamGroup, OrgTypeParentTeam:
		return true
	}
	return false
}

type EntityType string

const (
	EntityEmployee  EntityType = "employee"
	EntityTeam      EntityType = "team"
	EntityOrg       EntityType = "org"
	EntityPillar    EntityType = "pillar"
	EntityTeamGroup EntityType = "team_group"
)

func (e EntityType) String() string { return string(e) }

func (e EntityType) IsValid() bool {
	switch e {
	case EntityEmployee, EntityTeam, EntityOrg, EntityPillar, EntityTeamGroup:
		return true
	}
	return false
}
