package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/occam-bci/terraform-provider-metabase/metabase"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensures provider defined types fully satisfy framework interfaces.
var _ resource.ResourceWithImportState = &PermissionsGroupMembershipResource{}

// Creates a new permissions group membership resource.
func NewPermissionsGroupMembershipResource() resource.Resource {
	return &PermissionsGroupMembershipResource{
		MetabaseBaseResource{name: "permissions_group_membership"},
	}
}

// A resource handling a permissions group membership.
type PermissionsGroupMembershipResource struct {
	MetabaseBaseResource
}

// The Terraform model for a permissions group membership.
type PermissionsGroupMembershipResourceModel struct {
	Id             types.Int64 `tfsdk:"id"`               // The ID of the membership.
	UserId         types.Int64 `tfsdk:"user_id"`          // The ID of the user.
	GroupId        types.Int64 `tfsdk:"group_id"`         // The ID of the permissions group.
	IsGroupManager types.Bool  `tfsdk:"is_group_manager"` // Whether the user is a manager of this group.
}

func (r *PermissionsGroupMembershipResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Metabase permissions group membership links a user to a permissions group.",

		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "The ID of the membership (same as group_id).",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"user_id": schema.Int64Attribute{
				MarkdownDescription: "The ID of the user.",
				Required:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"group_id": schema.Int64Attribute{
				MarkdownDescription: "The ID of the permissions group.",
				Required:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"is_group_manager": schema.BoolAttribute{
				MarkdownDescription: "Whether the user is a manager of this group.",
				Optional:            true,
			},
		},
	}
}

func (r *PermissionsGroupMembershipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data *PermissionsGroupMembershipResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userId := int(data.UserId.ValueInt64())
	groupId := int(data.GroupId.ValueInt64())
	isGroupManager := false
	if !data.IsGroupManager.IsNull() && !data.IsGroupManager.IsUnknown() {
		isGroupManager = data.IsGroupManager.ValueBool()
	}

	// Read the user to get current memberships
	getUserResp, err := r.client.GetUserWithResponse(ctx, userId)
	resp.Diagnostics.Append(checkMetabaseResponse(getUserResp, err, []int{200}, "get user")...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Parse user with memberships
	// Use Body field which already contains the read bytes (HTTPResponse.Body is already closed)
	var userWithMemberships metabase.UserWithMemberships
	if err := json.Unmarshal(getUserResp.Body, &userWithMemberships); err != nil {
		resp.Diagnostics.AddError("Failed to parse user response", err.Error())
		return
	}

	// Check if membership already exists
	for _, membership := range userWithMemberships.UserGroupMemberships {
		if membership.Id == groupId {
			resp.Diagnostics.AddError("Membership already exists", fmt.Sprintf("User %d is already a member of group %d", userId, groupId))
			return
		}
	}

	// Add the new membership
	memberships := userWithMemberships.UserGroupMemberships
	memberships = append(memberships, metabase.UserGroupMembership{
		Id:             groupId,
		IsGroupManager: isGroupManager,
	})

	// Update user with new memberships
	email := userWithMemberships.Email
	firstName := userWithMemberships.FirstName
	lastName := userWithMemberships.LastName

	updateBody := metabase.UpdateUserBodyWithMemberships{
		Email:                &email,
		FirstName:            &firstName,
		LastName:             &lastName,
		UserGroupMemberships: &memberships,
	}

	jsonBody, err := json.Marshal(updateBody)
	if err != nil {
		resp.Diagnostics.AddError("Failed to marshal update request", err.Error())
		return
	}

	httpResp, err := r.client.DoHTTPRequest(ctx, "PUT", fmt.Sprintf("user/%d", userId), strings.NewReader(string(jsonBody)))
	if err != nil {
		resp.Diagnostics.AddError("Failed to create membership", err.Error())
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError("Failed to create membership", fmt.Sprintf("Status: %d, Body: %s", httpResp.StatusCode, string(bodyBytes)))
		return
	}

	// Set the ID to the group ID (since that's what identifies the membership)
	data.Id = types.Int64Value(int64(groupId))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PermissionsGroupMembershipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data *PermissionsGroupMembershipResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userId := int(data.UserId.ValueInt64())
	groupId := int(data.GroupId.ValueInt64())

	// Get user with memberships
	getUserResp, err := r.client.GetUserWithResponse(ctx, userId)
	if getUserResp != nil && getUserResp.StatusCode() == 404 {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(checkMetabaseResponse(getUserResp, err, []int{200}, "get user")...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Parse user with memberships
	// Use Body field which already contains the read bytes (HTTPResponse.Body is already closed)
	var userWithMemberships metabase.UserWithMemberships
	if err := json.Unmarshal(getUserResp.Body, &userWithMemberships); err != nil {
		resp.Diagnostics.AddError("Failed to parse user response", err.Error())
		return
	}

	// Check if membership still exists
	found := false
	for _, membership := range userWithMemberships.UserGroupMemberships {
		if membership.Id == groupId {
			found = true
			data.IsGroupManager = types.BoolValue(membership.IsGroupManager)
			break
		}
	}

	if !found {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PermissionsGroupMembershipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data *PermissionsGroupMembershipResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userId := int(data.UserId.ValueInt64())
	groupId := int(data.GroupId.ValueInt64())
	isGroupManager := data.IsGroupManager.ValueBool()

	// Read the user to get current memberships
	getUserResp, err := r.client.GetUserWithResponse(ctx, userId)
	resp.Diagnostics.Append(checkMetabaseResponse(getUserResp, err, []int{200}, "get user")...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Parse user with memberships
	// Use Body field which already contains the read bytes (HTTPResponse.Body is already closed)
	var userWithMemberships metabase.UserWithMemberships
	if err := json.Unmarshal(getUserResp.Body, &userWithMemberships); err != nil {
		resp.Diagnostics.AddError("Failed to parse user response", err.Error())
		return
	}

	// Update the membership
	memberships := userWithMemberships.UserGroupMemberships
	for i, membership := range memberships {
		if membership.Id == groupId {
			memberships[i].IsGroupManager = isGroupManager
			break
		}
	}

	// Update user with modified memberships
	email := userWithMemberships.Email
	firstName := userWithMemberships.FirstName
	lastName := userWithMemberships.LastName

	updateBody := metabase.UpdateUserBodyWithMemberships{
		Email:                &email,
		FirstName:            &firstName,
		LastName:             &lastName,
		UserGroupMemberships: &memberships,
	}

	jsonBody, err := json.Marshal(updateBody)
	if err != nil {
		resp.Diagnostics.AddError("Failed to marshal update request", err.Error())
		return
	}

	httpResp, err := r.client.DoHTTPRequest(ctx, "PUT", fmt.Sprintf("user/%d", userId), strings.NewReader(string(jsonBody)))
	if err != nil {
		resp.Diagnostics.AddError("Failed to update membership", err.Error())
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError("Failed to update membership", fmt.Sprintf("Status: %d, Body: %s", httpResp.StatusCode, string(bodyBytes)))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PermissionsGroupMembershipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data *PermissionsGroupMembershipResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userId := int(data.UserId.ValueInt64())
	groupId := int(data.GroupId.ValueInt64())

	// Read the user to get current memberships
	getUserResp, err := r.client.GetUserWithResponse(ctx, userId)
	resp.Diagnostics.Append(checkMetabaseResponse(getUserResp, err, []int{200}, "get user")...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Parse user with memberships
	// Use Body field which already contains the read bytes (HTTPResponse.Body is already closed)
	var userWithMemberships metabase.UserWithMemberships
	if err := json.Unmarshal(getUserResp.Body, &userWithMemberships); err != nil {
		resp.Diagnostics.AddError("Failed to parse user response", err.Error())
		return
	}

	// Remove the membership
	memberships := []metabase.UserGroupMembership{}
	for _, membership := range userWithMemberships.UserGroupMemberships {
		if membership.Id != groupId {
			memberships = append(memberships, membership)
		}
	}

	// Update user with removed membership
	email := userWithMemberships.Email
	firstName := userWithMemberships.FirstName
	lastName := userWithMemberships.LastName

	updateBody := metabase.UpdateUserBodyWithMemberships{
		Email:                &email,
		FirstName:            &firstName,
		LastName:             &lastName,
		UserGroupMemberships: &memberships,
	}

	jsonBody, err := json.Marshal(updateBody)
	if err != nil {
		resp.Diagnostics.AddError("Failed to marshal update request", err.Error())
		return
	}

	httpResp, err := r.client.DoHTTPRequest(ctx, "PUT", fmt.Sprintf("user/%d", userId), strings.NewReader(string(jsonBody)))
	if err != nil {
		resp.Diagnostics.AddError("Failed to delete membership", err.Error())
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError("Failed to delete membership", fmt.Sprintf("Status: %d, Body: %s", httpResp.StatusCode, string(bodyBytes)))
		return
	}
}

func (r *PermissionsGroupMembershipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "user_id:group_id"
	parts := strings.Split(req.ID, ":")
	if len(parts) != 2 {
		resp.Diagnostics.AddError("Invalid import ID", "Import ID must be in format 'user_id:group_id'")
		return
	}

	userId, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid user ID", err.Error())
		return
	}

	groupId, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid group ID", err.Error())
		return
	}

	data := &PermissionsGroupMembershipResourceModel{
		Id:      types.Int64Value(groupId),
		UserId:  types.Int64Value(userId),
		GroupId: types.Int64Value(groupId),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
