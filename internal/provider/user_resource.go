package provider

import (
	"context"

	"github.com/flovouin/terraform-provider-metabase/metabase"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensures provider defined types fully satisfy framework interfaces.
var _ resource.ResourceWithImportState = &UserResource{}

// Creates a new user resource.
func NewUserResource() resource.Resource {
	return &UserResource{
		MetabaseBaseResource{name: "user"},
	}
}

// A resource handling a user.
type UserResource struct {
	MetabaseBaseResource
}

// The Terraform model for a user.
type UserResourceModel struct {
	Id        types.Int64  `tfsdk:"id"`         // The ID of the user.
	Email     types.String `tfsdk:"email"`      // The email address of the user.
	FirstName types.String `tfsdk:"first_name"` // The first name of the user.
	LastName  types.String `tfsdk:"last_name"`  // The last name of the user.
	Password  types.String `tfsdk:"password"`   // The password for the user (optional).
}

func (r *UserResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Metabase user.",

		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "The ID of the user.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "The email address of the user.",
				Required:            true,
			},
			"first_name": schema.StringAttribute{
				MarkdownDescription: "The first name of the user.",
				Required:            true,
			},
			"last_name": schema.StringAttribute{
				MarkdownDescription: "The last name of the user.",
				Required:            true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "The password for the user (optional, only used during creation).",
				Optional:            true,
				Sensitive:           true,
			},
		},
	}
}

// Updates the given `UserResourceModel` from the `User` returned by the Metabase API.
func updateModelFromUser(u metabase.User, data *UserResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.Id = types.Int64Value(int64(u.Id))
	data.Email = types.StringValue(u.Email)
	data.FirstName = types.StringValue(u.FirstName)
	data.LastName = types.StringValue(u.LastName)

	return diags
}

func (r *UserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data *UserResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createBody := metabase.CreateUserBody{
		Email:     data.Email.ValueString(),
		FirstName: data.FirstName.ValueString(),
		LastName:  data.LastName.ValueString(),
	}

	// Add password if provided
	if !data.Password.IsNull() && !data.Password.IsUnknown() {
		password := data.Password.ValueString()
		createBody.Password = &password
	}

	createResp, err := r.client.CreateUserWithResponse(ctx, createBody)

	resp.Diagnostics.Append(checkMetabaseResponse(createResp, err, []int{200}, "create user")...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(updateModelFromUser(*createResp.JSON200, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data *UserResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := r.client.GetUserWithResponse(ctx, int(data.Id.ValueInt64()))

	resp.Diagnostics.Append(checkMetabaseResponse(getResp, err, []int{200, 204, 404}, "get user")...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The Metabase API can also return "no content" when the user has been deleted.
	if getResp.StatusCode() == 404 || getResp.StatusCode() == 204 {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(updateModelFromUser(*getResp.JSON200, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data *UserResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	email := data.Email.ValueString()
	firstName := data.FirstName.ValueString()
	lastName := data.LastName.ValueString()

	updateBody := metabase.UpdateUserBody{
		Email:     &email,
		FirstName: &firstName,
		LastName:  &lastName,
	}

	updateResp, err := r.client.UpdateUserWithResponse(ctx, int(data.Id.ValueInt64()), updateBody)

	resp.Diagnostics.Append(checkMetabaseResponse(updateResp, err, []int{200}, "update user")...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(updateModelFromUser(*updateResp.JSON200, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data *UserResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteResp, err := r.client.DeleteUserWithResponse(ctx, int(data.Id.ValueInt64()))

	resp.Diagnostics.Append(checkMetabaseResponse(deleteResp, err, []int{204}, "delete user")...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *UserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importStatePassthroughIntegerId(ctx, req, resp)
}
