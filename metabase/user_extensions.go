package metabase

// UserGroupMembership represents a user's membership in a group
type UserGroupMembership struct {
	Id             int  `json:"id"`                         // The group ID
	IsGroupManager bool `json:"is_group_manager,omitempty"` // Whether the user is a manager of this group (only present if advanced permissions enabled)
}

// UpdateUserBodyWithMemberships extends UpdateUserBody with group memberships
type UpdateUserBodyWithMemberships struct {
	Email               *string                `json:"email,omitempty"`
	FirstName           *string                `json:"first_name,omitempty"`
	IsSuperuser         *bool                  `json:"is_superuser,omitempty"`
	LastName            *string                `json:"last_name,omitempty"`
	UserGroupMemberships *[]UserGroupMembership `json:"user_group_memberships,omitempty"`
}

// UserWithMemberships extends User with group memberships
type UserWithMemberships struct {
	CommonName           *string                `json:"common_name,omitempty"`
	Email                string                 `json:"email"`
	FirstName            string                 `json:"first_name"`
	Id                   int                    `json:"id"`
	IsActive             *bool                  `json:"is_active,omitempty"`
	IsSuperuser          *bool                  `json:"is_superuser,omitempty"`
	LastName             string                 `json:"last_name"`
	UserGroupMemberships []UserGroupMembership  `json:"user_group_memberships,omitempty"`
}
