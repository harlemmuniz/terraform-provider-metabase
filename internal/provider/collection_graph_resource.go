package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/occam-bci/terraform-provider-metabase/metabase"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Ensures provider defined types fully satisfy framework interfaces.
var _ resource.ResourceWithImportState = &CollectionGraphResource{}

// Creates a new collection graph resource.
func NewCollectionGraphResource() resource.Resource {
	return &CollectionGraphResource{
		MetabaseBaseResource{name: "collection_graph"},
	}
}

// A resource handling the entire permissions graph for Metabase collections.
type CollectionGraphResource struct {
	MetabaseBaseResource
}

// The Terraform model for the graph.
// Instead of representing the graph as a map, it is stored as a list of edges (group ↔️ collection permission).
// This is easier to model using Terraform schemas.
type CollectionGraphResourceModel struct {
	Revision                        types.Int64 `tfsdk:"revision"`                          // The revision number for the graph, set by Metabase.
	IgnoredGroups                   types.Set   `tfsdk:"ignored_groups"`                  // The list of groups that should be ignored when updating permissions.
	Permissions                     types.Set   `tfsdk:"permissions"`                     // The list of permissions (edges) in the graph.
	ApplyChildCollectionsPermissions types.Bool `tfsdk:"apply_child_collections_permissions"` // Whether to automatically apply READ permissions to all child collections of Public (5) and Draft (4).
}

// The model for a single edge in the permissions graph.
type CollectionPermission struct {
	Group      types.Int64  `tfsdk:"group"`      // The permissions group to which the permission applies.
	Collection types.String `tfsdk:"collection"` // The collection to which the permission applies. The collection is a string because it could be the `root` collection.
	Permission types.String `tfsdk:"permission"` // The permission level (read or write).
}

func (r *CollectionGraphResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `The graph of permissions between permissions groups and collections.

Metabase exposes a single resource to define all permissions related to collections. This means a single collection graph resource should be defined in the entire Terraform configuration.

The collection graph cannot be created or deleted. Trying to create it will result in an error. It should be imported instead. Trying to delete the resource will succeed with no impact on Metabase (it is a no-op).

Permissions for the Administrators group cannot be changed. To avoid issues during the update, all permissions for the Administrators group are ignored by default. This behavior can be changed using the ignored groups attribute.`,

		Attributes: map[string]schema.Attribute{
			"revision": schema.Int64Attribute{
				MarkdownDescription: "The revision number for the graph.",
				Computed:            true,
			},
			"ignored_groups": schema.SetAttribute{
				ElementType:         types.Int64Type,
				MarkdownDescription: "The list of group IDs that should be ignored when reading and updating permissions. By default, this contains the Administrators group (`[2]`).",
				Optional:            true,
			},
			"permissions": schema.SetNestedAttribute{
				MarkdownDescription: "A list of permissions for a given group and collection. A (group, collection) pair should appear only once in the list.",
				Required:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"group": schema.Int64Attribute{
							MarkdownDescription: "The ID of the group to which the permission applies.",
							Required:            true,
						},
						"collection": schema.StringAttribute{
							MarkdownDescription: "The ID of the collection to which the permission applies.",
							Required:            true,
						},
						"permission": schema.StringAttribute{
							MarkdownDescription: "The level of permission (`read` or `write`).",
							Required:            true,
						},
					},
				},
			},
			"apply_child_collections_permissions": schema.BoolAttribute{
				MarkdownDescription: "When enabled (default: true), automatically applies READ permissions to all child collections of Public (ID 5) and Draft (ID 4) collections for all groups that have permissions defined. This ensures that groups can navigate through all subcollections.",
				Optional:            true,
			},
		},
	}
}

// Makes a single permission (edge) object to be stored in the model.
func makePermissionObjectFromPermission(ctx context.Context, groupId string, colId string, p metabase.CollectionPermissionLevel) (*types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	// Groups are received as strings because they are keys of a JSON map, but they should all correspond to integers.
	groupIdInt, err := strconv.Atoi(groupId)
	if err != nil {
		diags.AddError("Could not convert group ID to int.", err.Error())
		return nil, diags
	}

	permissionObject, objectDiags := types.ObjectValueFrom(ctx, map[string]attr.Type{
		"group":      types.Int64Type,
		"collection": types.StringType,
		"permission": types.StringType,
	}, CollectionPermission{
		Group:      types.Int64Value(int64(groupIdInt)),
		Collection: types.StringValue(colId),
		Permission: types.StringValue(string(p)),
	})
	diags.Append(objectDiags...)
	if diags.HasError() {
		return nil, diags
	}

	return &permissionObject, diags
}

// Updates the given `CollectionGraphResourceModel` from the `CollectionPermissionsGraph` returned by the Metabase API.
func updateModelFromCollectionPermissionsGraph(ctx context.Context, g metabase.CollectionPermissionsGraph, data *CollectionGraphResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.Revision = types.Int64Value(int64(g.Revision))

	ignoredGroups, groupsDiags := getIgnoredPermissionsGroups(ctx, data.IgnoredGroups)
	diags.Append(groupsDiags...)
	if diags.HasError() {
		return diags
	}

	permissionsList := make([]attr.Value, 0, len(data.Permissions.Elements()))
	for groupId, colPermissionsMap := range g.Groups {
		// Permissions for ignored groups are not stored in the state for clarity.
		if ignoredGroups[groupId] {
			continue
		}

		for colId, permission := range colPermissionsMap {
			// Skipping `none` permissions for clarity. Only read or write permissions should be specified.
			if permission == metabase.CollectionPermissionLevelNone {
				continue
			}

			permissionObject, objDiags := makePermissionObjectFromPermission(ctx, groupId, colId, permission)
			diags.Append(objDiags...)
			if diags.HasError() {
				return diags
			}

			permissionsList = append(permissionsList, *permissionObject)
		}
	}

	permissionsSet, setDiags := types.SetValue(types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"group":      types.Int64Type,
			"collection": types.StringType,
			"permission": types.StringType,
		},
	}, permissionsList)
	diags.Append(setDiags...)
	if diags.HasError() {
		return diags
	}

	data.Permissions = permissionsSet

	return diags
}

// CollectionInfo holds information about a collection
type CollectionInfo struct {
	ID       int
	ParentID int
	Name     string
	Location string // Hierarchical location path (e.g., "/5/14/", "/5/16/60/")
}

// Fetches all collections with their parent IDs and names
// Returns a map of collectionId -> CollectionInfo for all child collections
func fetchChildCollections(ctx context.Context, client *metabase.ClientWithResponses) (map[int]CollectionInfo, diag.Diagnostics) {
	var diags diag.Diagnostics
	childCollections := make(map[int]CollectionInfo)

	// Fetch all collections
	listResp, err := client.ListCollectionsWithResponse(ctx, &metabase.ListCollectionsParams{})
	if err != nil {
		diags.AddError("Failed to list collections", err.Error())
		return childCollections, diags
	}

	if listResp.StatusCode() != 200 {
		diags.AddError("Failed to list collections", fmt.Sprintf("Status code: %d", listResp.StatusCode()))
		return childCollections, diags
	}

	var collections []metabase.Collection
	if err := json.Unmarshal(listResp.Body, &collections); err != nil {
		diags.AddError("Failed to parse collections response", err.Error())
		return childCollections, diags
	}

	// Build map of all collections with their parent IDs and names
	// We derive parent ID from the location field since the API may not return parent_id directly
	// Location format: /parentId/ or /grandparentId/parentId/ etc.
	// We exclude personal collections (PersonalOwnerId != nil)
	for _, collection := range collections {
		// Skip personal collections
		if collection.PersonalOwnerId != nil {
			continue
		}
		
		// Get location - this is required to derive parent ID
		location := ""
		if collection.Location != nil {
			location = *collection.Location
		}
		
		// Skip root collections (location is "/" or empty)
		if location == "" || location == "/" {
			continue
		}
		
		// Derive parent ID from location
		// Location format: /5/ means parent is 5, /5/16/ means parent is 16
		// The parent is the LAST element in the location path
		var parentId int = -1
		locationParts := strings.Split(strings.Trim(location, "/"), "/")
		if len(locationParts) > 0 {
			lastPart := locationParts[len(locationParts)-1]
			if parsedParent, err := strconv.Atoi(lastPart); err == nil {
				parentId = parsedParent
			}
		}
		
		// Skip if we couldn't derive a parent ID
		if parentId < 0 {
			continue
		}
		
		// Collection.Id is a union type that can be int or string
		// Try to unmarshal as int first
		var collectionIdInt int
		idBytes, _ := json.Marshal(collection.Id)
		if err := json.Unmarshal(idBytes, &collectionIdInt); err == nil {
			childCollections[collectionIdInt] = CollectionInfo{
				ID:       collectionIdInt,
				ParentID: parentId,
				Name:     collection.Name,
				Location: location,
			}
		} else {
			// Try as string, but skip "root"
			var collectionIdStr string
			if err := json.Unmarshal(idBytes, &collectionIdStr); err == nil && collectionIdStr != "root" {
				// Try to parse string as int
				if parsedId, err := strconv.Atoi(collectionIdStr); err == nil {
					childCollections[parsedId] = CollectionInfo{
						ID:       parsedId,
						ParentID: parentId,
						Name:     collection.Name,
						Location: location,
					}
				}
			}
		}
	}

	return childCollections, diags
}

// Fetches group names for given group IDs
func fetchGroupNames(ctx context.Context, client *metabase.ClientWithResponses, groupIds []int64) (map[int64]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	groupNames := make(map[int64]string)

	for _, groupId := range groupIds {
		getResp, err := client.GetPermissionsGroupWithResponse(ctx, int(groupId))
		if err != nil {
			diags.AddWarning(fmt.Sprintf("Failed to get group %d", groupId), err.Error())
			continue
		}

		if getResp.StatusCode() == 200 && getResp.JSON200 != nil {
			groupNames[groupId] = getResp.JSON200.Name
		}
	}

	return groupNames, diags
}

// Normalizes a name for comparison: lowercase, remove spaces, replace underscores/hyphens with nothing
func normalizeName(name string) string {
	// Convert to lowercase
	normalized := strings.ToLower(name)
	
	// Replace spaces, underscores, and hyphens with nothing
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	
	// Remove any non-alphanumeric characters
	var result strings.Builder
	for _, r := range normalized {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			result.WriteRune(r)
		}
	}
	
	return result.String()
}

// isCollectionUnderGroup verifies if a collection is below a specific group
// Example: isCollectionUnderGroup("/5/16/60/69/", 5, 16) returns true (16 is the group ID)
// The location format is: /rootCollectionId/groupId/... where groupId is the numeric group ID
func isCollectionUnderGroup(location string, rootCollectionId int, groupId int) bool {
	if location == "" {
		return false
	}
	
	// Pattern expected: /rootCollectionId/groupId/...
	// This matches locations like /5/16/, /5/16/60/, /5/16/60/69/, etc.
	// where 16 is the numeric group ID (not the collection ID)
	pattern := fmt.Sprintf("/%d/%d/", rootCollectionId, groupId)
	return strings.HasPrefix(location, pattern)
}

// Creates the `CollectionPermissionsGraph` to send to the API, based on the Terraform plan, but also the existing state
// (if permissions need to be removed).
func makeCollectionPermissionsGraphFromModel(ctx context.Context, data CollectionGraphResourceModel, state *CollectionGraphResourceModel, client *metabase.ClientWithResponses) (*metabase.CollectionPermissionsGraph, diag.Diagnostics) {
	var diags diag.Diagnostics

	revision := int(data.Revision.ValueInt64())

	permissions := make([]CollectionPermission, 0, len(data.Permissions.Elements()))
	diags.Append(data.Permissions.ElementsAs(ctx, &permissions, false)...)
	if diags.HasError() {
		return nil, diags
	}

	// Creating the permissions map from the plan.
	groups := make(map[string]metabase.CollectionPermissionsGraphCollectionPermissionsMap, len(permissions))
	for _, p := range permissions {
		if p.Group.IsNull() {
			diags.AddError("Unexpected null group in permission.", "")
			return nil, diags
		}
		if p.Collection.IsNull() {
			diags.AddError("Unexpected null collection in permission.", "")
			return nil, diags
		}
		groupId := strconv.FormatInt(p.Group.ValueInt64(), 10)
		collectionId := p.Collection.ValueString()

		colPermMap, ok := groups[groupId]
		if !ok {
			colPermMap = make(metabase.CollectionPermissionsGraphCollectionPermissionsMap)
			groups[groupId] = colPermMap
		}

		_, permExists := colPermMap[collectionId]
		if permExists {
			diags.AddError("Found duplicate permission definition.", fmt.Sprintf("Group ID: %s, Collection ID: %s.", groupId, collectionId))
			return nil, diags
		}

		colPermMap[collectionId] = metabase.CollectionPermissionLevel(p.Permission.ValueString())
	}

	// If apply_child_collections_permissions is enabled (default: true), fetch and add permissions for child collections
	applyChildPermissions := true
	if !data.ApplyChildCollectionsPermissions.IsNull() && !data.ApplyChildCollectionsPermissions.IsUnknown() {
		applyChildPermissions = data.ApplyChildCollectionsPermissions.ValueBool()
	}

	if applyChildPermissions && client != nil {
		childCollectionsMap, childDiags := fetchChildCollections(ctx, client)
		diags.Append(childDiags...)
		if !diags.HasError() {
			// Get all unique group IDs from the permissions
			groupIdsSet := make(map[int64]bool)
			groupIdsList := make([]int64, 0)
			for _, p := range permissions {
				if !p.Group.IsNull() {
					groupId := p.Group.ValueInt64()
					if !groupIdsSet[groupId] {
						groupIdsSet[groupId] = true
						groupIdsList = append(groupIdsList, groupId)
					}
				}
			}

			// Fetch group names for name matching
			groupNames, groupNamesDiags := fetchGroupNames(ctx, client, groupIdsList)
			diags.Append(groupNamesDiags...)

			// Build a map of parent collection -> permission level for each group
			// This allows us to inherit permissions from parent collections
			parentPermissions := make(map[string]map[string]metabase.CollectionPermissionLevel) // groupId -> parentCollectionId -> permission
			for groupId := range groupIdsSet {
				groupIdStr := strconv.FormatInt(groupId, 10)
				parentPermissions[groupIdStr] = make(map[string]metabase.CollectionPermissionLevel)
				
				colPermMap, ok := groups[groupIdStr]
				if ok {
					for parentColId, perm := range colPermMap {
						parentPermissions[groupIdStr][parentColId] = perm
					}
				}
			}

			// First, identify group collections (direct children of 4 and 5)
			// Map: rootCollectionId -> groupId -> groupCollectionId
			groupCollections := make(map[int]map[int64]int) // rootCollectionId -> groupId -> collectionId
			// Also create reverse mapping: collectionId -> groupId (for recursive permission lookup)
			collectionIdToGroupId := make(map[int]int64)
			
			for childCollectionId, collectionInfo := range childCollectionsMap {
				parentId := collectionInfo.ParentID
				if parentId == 5 || parentId == 4 {
					// This is a group collection - find which group it corresponds to
					childCollectionName := collectionInfo.Name
					for groupId := range groupIdsSet {
						groupName, hasGroupName := groupNames[groupId]
						if hasGroupName {
							normalizedGroupName := normalizeName(groupName)
							normalizedCollectionName := normalizeName(childCollectionName)
							if normalizedGroupName == normalizedCollectionName {
								if groupCollections[parentId] == nil {
									groupCollections[parentId] = make(map[int64]int)
								}
								groupCollections[parentId][groupId] = childCollectionId
								// Store reverse mapping: collection ID -> group ID
								collectionIdToGroupId[childCollectionId] = groupId
								break
							}
						}
					}
				}
			}

			// Apply permissions to direct children of Public (5) or Draft (4) - group collections
			for childCollectionId, collectionInfo := range childCollectionsMap {
				parentId := collectionInfo.ParentID
				
				// Only process direct children of Public (5) or Draft (4)
				if parentId != 5 && parentId != 4 {
					continue
				}
				
				childCollectionIdStr := strconv.Itoa(childCollectionId)
				childCollectionName := collectionInfo.Name

				for groupId := range groupIdsSet {
					groupIdStr := strconv.FormatInt(groupId, 10)
					
					colPermMap, ok := groups[groupIdStr]
					if !ok {
						colPermMap = make(metabase.CollectionPermissionsGraphCollectionPermissionsMap)
						groups[groupIdStr] = colPermMap
					}

					// Check if this is an explicit permission (exists in the Terraform plan)
					isExplicitPermission := false
					for _, p := range permissions {
						if !p.Group.IsNull() && !p.Collection.IsNull() &&
						   p.Group.ValueInt64() == groupId &&
						   p.Collection.ValueString() == childCollectionIdStr {
							isExplicitPermission = true
							break
						}
					}

					// Only apply automatic logic if not an explicit permission
					if !isExplicitPermission {
						existingPerm, exists := colPermMap[childCollectionIdStr]
						
						// Check if group name matches collection name (normalized)
						groupName, hasGroupName := groupNames[groupId]
						permission := metabase.CollectionPermissionLevelRead // Default to READ for other groups
						
						if hasGroupName {
							normalizedGroupName := normalizeName(groupName)
							normalizedCollectionName := normalizeName(childCollectionName)
							
							// If names match, give WRITE permission (will be inherited to all subcollections by Metabase)
							if normalizedGroupName == normalizedCollectionName {
								permission = metabase.CollectionPermissionLevelWrite
							}
						}
						
						// Apply permission if it doesn't exist, or upgrade READ to WRITE if names match
						// Metabase will automatically inherit this permission to all subcollections
						if !exists || (exists && existingPerm == metabase.CollectionPermissionLevelRead && permission == metabase.CollectionPermissionLevelWrite) {
							colPermMap[childCollectionIdStr] = permission
						}
					}
				}
			}

			// Now apply permissions recursively to ALL collections below group collections using Location
			// Location format: /rootCollectionId/groupCollectionId/... 
			// where groupCollectionId is the ID of the collection that belongs to a group
			// We need to find which group owns that collection using collectionIdToGroupId map
			for childCollectionId, collectionInfo := range childCollectionsMap {
				location := collectionInfo.Location
				if location == "" {
					continue
				}
				
				// Extract the group collection ID from location for Public (5) and Draft (4)
				// Location format: /5/collectionId/... or /4/collectionId/...
				var groupCollectionIdFromLocation int = -1
				
				// Check if location starts with /5/ (Public) or /4/ (Draft)
				if strings.HasPrefix(location, "/5/") {
					parts := strings.Split(strings.TrimPrefix(location, "/5/"), "/")
					if len(parts) > 0 && parts[0] != "" {
						if parsedId, err := strconv.Atoi(parts[0]); err == nil {
							groupCollectionIdFromLocation = parsedId
						}
					}
				} else if strings.HasPrefix(location, "/4/") {
					parts := strings.Split(strings.TrimPrefix(location, "/4/"), "/")
					if len(parts) > 0 && parts[0] != "" {
						if parsedId, err := strconv.Atoi(parts[0]); err == nil {
							groupCollectionIdFromLocation = parsedId
						}
					}
				}
				
				// Skip if we couldn't extract a group collection ID from location
				if groupCollectionIdFromLocation < 0 {
					continue
				}
				
				// Find the owning group ID using the reverse mapping
				owningGroupId, hasOwningGroup := collectionIdToGroupId[groupCollectionIdFromLocation]
				if !hasOwningGroup {
					// This collection is under a path we don't manage (group collection not in Terraform plan)
					continue
				}
				
				childCollectionIdStr := strconv.Itoa(childCollectionId)
				
				// Check if this child collection is explicitly in the Terraform plan
				// If it is, we should NOT apply recursive permissions (let Terraform manage it explicitly)
				isChildCollectionInPlan := false
				for _, p := range permissions {
					if !p.Group.IsNull() && !p.Collection.IsNull() &&
					   p.Collection.ValueString() == childCollectionIdStr {
						isChildCollectionInPlan = true
						break
					}
				}
				
				// Skip recursive permissions if the child collection is explicitly in the Terraform plan
				if isChildCollectionInPlan {
					continue
				}
				
				// Apply permissions for ALL groups that are in the Terraform plan
				// Owning group gets WRITE, all others get READ
				for groupId := range groupIdsSet {
					groupIdStr := strconv.FormatInt(groupId, 10)
					
					colPermMap, ok := groups[groupIdStr]
					if !ok {
						colPermMap = make(metabase.CollectionPermissionsGraphCollectionPermissionsMap)
						groups[groupIdStr] = colPermMap
					}
					
					// Check if this is an explicit permission (exists in the Terraform plan)
					isExplicitPermission := false
					for _, p := range permissions {
						if !p.Group.IsNull() && !p.Collection.IsNull() &&
						   p.Group.ValueInt64() == groupId &&
						   p.Collection.ValueString() == childCollectionIdStr {
							isExplicitPermission = true
							break
						}
					}
					
					// Only apply recursive permissions if not an explicit permission
					if !isExplicitPermission {
						// Apply recursive rule: owning group gets WRITE, others get READ
						var permission metabase.CollectionPermissionLevel
						if groupId == owningGroupId {
							permission = metabase.CollectionPermissionLevelWrite
						} else {
							permission = metabase.CollectionPermissionLevelRead
						}
						colPermMap[childCollectionIdStr] = permission
					}
				}
			}
		}
	}

	if state != nil {
		// When making the request to the Metabase API, the currently known revision number should be passed.
		// It will be increased and returned by Metabase.
		revision = int(state.Revision.ValueInt64())
	}

	return &metabase.CollectionPermissionsGraph{
		Revision: revision,
		Groups:   groups,
	}, diags
}

func (r *CollectionGraphResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data *CollectionGraphResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Initialize apply_child_collections_permissions with default value if not set
	if data.ApplyChildCollectionsPermissions.IsNull() || data.ApplyChildCollectionsPermissions.IsUnknown() {
		data.ApplyChildCollectionsPermissions = types.BoolValue(true)
	}

	// The Metabase permissions graph always exists, so "create" actually means
	// applying the permissions to the existing graph. This allows:
	// 1. Initial import via terraform import
	// 2. terraform apply -replace to force re-application of permissions
	
	// First, get the current revision from Metabase
	getResp, err := r.client.GetCollectionPermissionsGraphWithResponse(ctx)
	resp.Diagnostics.Append(checkMetabaseResponse(getResp, err, []int{200}, "read collection graph")...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Set the current revision for the update
	currentRevision := getResp.JSON200.Revision
	data.Revision = types.Int64Value(int64(currentRevision))

	// Create a temporary state with the current revision to pass to makeCollectionPermissionsGraphFromModel
	tempState := &CollectionGraphResourceModel{
		Revision: types.Int64Value(int64(currentRevision)),
	}

	// Build the permissions graph including recursive permissions if enabled
	body, graphDiags := makeCollectionPermissionsGraphFromModel(ctx, *data, tempState, r.client)
	resp.Diagnostics.Append(graphDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Apply the permissions to Metabase
	updateResp, err := r.client.ReplaceCollectionPermissionsGraphWithResponse(ctx, *body)
	resp.Diagnostics.Append(checkMetabaseResponse(updateResp, err, []int{200}, "update collection graph")...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Update the revision from the response
	data.Revision = types.Int64Value(int64(updateResp.JSON200.Revision))

	// Save the state with only explicit permissions (not the recursive ones)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// filterRecursivePermissions removes recursive permissions from the state
// Recursive permissions are those for child collections under /5/{groupId}/* or /4/{groupId}/*
// that are not the group collection itself (which is a direct child of 5 or 4)
func filterRecursivePermissions(ctx context.Context, permissions types.Set, client *metabase.ClientWithResponses) (types.Set, diag.Diagnostics) {
	var diags diag.Diagnostics

	// Get all collections to identify which are child collections
	childCollectionsMap, fetchDiags := fetchChildCollections(ctx, client)
	diags.Append(fetchDiags...)
	if diags.HasError() {
		return permissions, diags
	}

	// Build a set of child collection IDs (collections that are nested under group collections)
	// Group collections are direct children of Public (5) or Draft (4) - they have location /5/ or /4/
	// Nested collections are children of group collections - they have location /5/X/, /5/X/Y/, /4/X/, etc.
	childCollectionIds := make(map[string]bool)
	for collectionId, collectionInfo := range childCollectionsMap {
		location := collectionInfo.Location
		// Skip collections with no parent (shouldn't happen but be safe)
		if collectionInfo.ParentID == 0 {
			continue
		}
		// Check if this is a nested collection under Public (5) or Draft (4)
		// Location format:
		//   /5/ = group collection (1 part) - direct child of Public
		//   /5/16/ = nested collection (2 parts) - child of group collection 16
		//   /5/16/60/ = deeper nested (3 parts) - grandchild
		// We want to filter out all nested collections (2+ parts)
		if strings.HasPrefix(location, "/5/") || strings.HasPrefix(location, "/4/") {
			parts := strings.Split(strings.Trim(location, "/"), "/")
			// len(parts) == 1: group collection (e.g., /5/ -> ["5"] is wrong, /5/ actually means parent is 5)
			// Actually, if location is /5/, that means the collection's parent is 5 (Public)
			// If location is /5/16/, the parent is 16, which is under 5
			// So:
			//   - Location /5/ -> parts = ["5"] -> 1 part -> this is a GROUP collection
			//   - Location /5/16/ -> parts = ["5", "16"] -> 2 parts -> this is NESTED
			//   - Location /5/16/60/ -> parts = ["5", "16", "60"] -> 3 parts -> this is NESTED
			if len(parts) > 1 {
				// This is a nested collection, not a group collection
				childCollectionIds[strconv.Itoa(collectionId)] = true
			}
		}
	}

	// Filter permissions, keeping only those that are NOT for child collections
	permissionsList := make([]attr.Value, 0)
	elements := permissions.Elements()
	for _, elem := range elements {
		var perm CollectionPermission
		if diags.Append(elem.(types.Object).As(ctx, &perm, basetypes.ObjectAsOptions{})...); diags.HasError() {
			return permissions, diags
		}

		if !perm.Collection.IsNull() {
			collectionId := perm.Collection.ValueString()
			// Keep permission if it's NOT for a child collection
			if !childCollectionIds[collectionId] {
				permissionsList = append(permissionsList, elem)
			}
		} else {
			// Keep permission if collection is null (shouldn't happen, but be safe)
			permissionsList = append(permissionsList, elem)
		}
	}

	permissionsSet, setDiags := types.SetValue(types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"group":      types.Int64Type,
			"collection": types.StringType,
			"permission": types.StringType,
		},
	}, permissionsList)
	diags.Append(setDiags...)
	if diags.HasError() {
		return permissions, diags
	}

	return permissionsSet, diags
}

func (r *CollectionGraphResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data *CollectionGraphResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Initialize apply_child_collections_permissions with default value if not set
	if data.ApplyChildCollectionsPermissions.IsNull() || data.ApplyChildCollectionsPermissions.IsUnknown() {
		data.ApplyChildCollectionsPermissions = types.BoolValue(true)
	}

	// IMPORTANT: Read should return ONLY the explicit permissions from the Terraform config,
	// NOT the recursive permissions. The recursive permissions are applied automatically
	// during Apply but are not managed by Terraform state. This way:
	// 1. Plan only shows explicit permissions (no recursive ones)
	// 2. Apply applies explicit + recursive permissions to Metabase
	// 3. Read returns only explicit permissions (so Plan doesn't try to remove recursive ones)
	
	// Filter out recursive permissions from the current state
	filteredPermissions, filterDiags := filterRecursivePermissions(ctx, data.Permissions, r.client)
	resp.Diagnostics.Append(filterDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Permissions = filteredPermissions

	// Update the revision from the API
	getResp, err := r.client.GetCollectionPermissionsGraphWithResponse(ctx)
	resp.Diagnostics.Append(checkMetabaseResponse(getResp, err, []int{200}, "read collection graph")...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Revision = types.Int64Value(int64(getResp.JSON200.Revision))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *CollectionGraphResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data *CollectionGraphResourceModel
	var state *CollectionGraphResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Initialize apply_child_collections_permissions with default value if not set
	if data.ApplyChildCollectionsPermissions.IsNull() || data.ApplyChildCollectionsPermissions.IsUnknown() {
		data.ApplyChildCollectionsPermissions = types.BoolValue(true)
	}

	// Check basic changes first
	permissionsChanged := !data.Permissions.Equal(state.Permissions)
	flagChanged := !data.ApplyChildCollectionsPermissions.IsNull() && !state.ApplyChildCollectionsPermissions.IsNull() && !data.ApplyChildCollectionsPermissions.Equal(state.ApplyChildCollectionsPermissions)
	
	// Only update if explicit permissions changed or flag changed
	// When updating, recursive permissions will be automatically applied if enabled
	var body *metabase.CollectionPermissionsGraph
	if permissionsChanged || flagChanged {
		// Calculate the graph including recursive permissions if enabled
		var diags diag.Diagnostics
		body, diags = makeCollectionPermissionsGraphFromModel(ctx, *data, state, r.client)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		updateResp, err := r.client.ReplaceCollectionPermissionsGraphWithResponse(ctx, *body)

		resp.Diagnostics.Append(checkMetabaseResponse(updateResp, err, []int{200}, "update collection graph")...)
		if resp.Diagnostics.HasError() {
			return
		}

		// IMPORTANT: After applying, we keep only the explicit permissions in the state,
		// NOT the recursive ones. The recursive permissions are applied to Metabase but
		// are not managed by Terraform state. This ensures Plan doesn't try to remove them.
		// Update only the revision, keep the explicit permissions from the plan
		data.Revision = types.Int64Value(int64(updateResp.JSON200.Revision))
	} else {
		// If no update was performed, the current revision number is still valid.
		data.Revision = state.Revision
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *CollectionGraphResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddWarning(
		"Delete operation is not supported for the Metabase collection permissions graph.",
		"The permission graph has been left intact and is no longer part of the Terraform state.",
	)
}

func (r *CollectionGraphResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	revision, err := strconv.Atoi(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to convert revision to an integer.", req.ID)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("revision"), revision)...)
}
