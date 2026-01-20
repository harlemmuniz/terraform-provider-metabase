# Terraform Metabase Provider

Terraform provider for managing Metabase users, groups, and collections.

## Installation

Add to your Terraform configuration:

```hcl
terraform {
  required_providers {
    metabase = {
      source  = "harlemmuniz/metabase"
      version = "1.0.0"
    }
  }
}

provider "metabase" {
  endpoint = "https://metabase.example.com/api"
  api_key  = var.metabase_api_key
}
```

## Resources

- `metabase_user` - Manage Metabase users
- `metabase_permissions_group` - Manage permission groups
- `metabase_collection` - Manage collections

## Example Usage

```hcl
resource "metabase_user" "example" {
  email      = "user@example.com"
  first_name = "John"
  last_name  = "Doe"
}

resource "metabase_permissions_group" "example" {
  name = "Analytics Team"
}

resource "metabase_collection" "example" {
  name        = "Team Analytics"
  description = "Collection for analytics team"
}
```

## Development

Requirements:
- Go 1.24+
- Terraform 1.0+

Build:
```bash
go build -o terraform-provider-metabase
```

## License

This provider is based on the original work by [flovouin](https://github.com/flovouin/terraform-provider-metabase).
