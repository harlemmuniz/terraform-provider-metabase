package provider

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func testAccUserResource(name string, email string, firstName string, lastName string) string {
	return fmt.Sprintf(`
resource "metabase_user" "%s" {
  email      = "%s"
  first_name = "%s"
  last_name  = "%s"
}
`,
		name,
		email,
		firstName,
		lastName,
	)
}

func testAccCheckUserExists(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("Failed to find resource %s in state.", resourceName)
		}

		userId, err := strconv.ParseInt(rs.Primary.ID, 10, 64)
		if err != nil {
			return err
		}

		response, err := testAccMetabaseClient.GetUserWithResponse(context.Background(), int(userId))
		if err != nil {
			return err
		}
		if response.StatusCode() != 200 {
			return fmt.Errorf("Received unexpected response from the Metabase API when getting user.")
		}

		if rs.Primary.Attributes["email"] != response.JSON200.Email {
			return fmt.Errorf("Terraform resource and API response do not match for user email.")
		}

		if rs.Primary.Attributes["first_name"] != response.JSON200.FirstName {
			return fmt.Errorf("Terraform resource and API response do not match for user first_name.")
		}

		if rs.Primary.Attributes["last_name"] != response.JSON200.LastName {
			return fmt.Errorf("Terraform resource and API response do not match for user last_name.")
		}

		return nil
	}
}

func testAccCheckUserDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "metabase_user" {
			continue
		}

		userId, err := strconv.ParseInt(rs.Primary.ID, 10, 64)
		if err != nil {
			return err
		}

		response, err := testAccMetabaseClient.GetUserWithResponse(context.Background(), int(userId))
		if err != nil {
			return err
		}
		if response.StatusCode() == 404 {
			return nil
		}

		return fmt.Errorf("User %s still exists.", rs.Primary.ID)
	}

	return nil
}

func TestAccUserResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckUserDestroy,
		Steps: []resource.TestStep{
			{
				Config: providerConfig + testAccUserResource("test", "test.user@example.com", "Test", "User"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckUserExists("metabase_user.test"),
					resource.TestCheckResourceAttrSet("metabase_user.test", "id"),
					resource.TestCheckResourceAttr("metabase_user.test", "email", "test.user@example.com"),
					resource.TestCheckResourceAttr("metabase_user.test", "first_name", "Test"),
					resource.TestCheckResourceAttr("metabase_user.test", "last_name", "User"),
				),
			},
			{
				ResourceName: "metabase_user.test",
				ImportState:  true,
			},
			{
				Config: providerConfig + testAccUserResource("test", "test.user@example.com", "Updated", "Name"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("metabase_user.test", "id"),
					resource.TestCheckResourceAttr("metabase_user.test", "email", "test.user@example.com"),
					resource.TestCheckResourceAttr("metabase_user.test", "first_name", "Updated"),
					resource.TestCheckResourceAttr("metabase_user.test", "last_name", "Name"),
				),
			},
		},
	})
}
