#!/bin/bash
set -e

echo "Preparing to push terraform-provider-metabase to GitHub..."
echo ""
echo "Make sure you have created a private repository at:"
echo "https://github.com/occam-bci/terraform-provider-metabase"
echo ""
read -p "Press Enter to continue or Ctrl+C to cancel..."

# Change remote to occam-bci
echo "Updating git remote..."
git remote set-url origin git@github.com:occam-bci/terraform-provider-metabase.git

# Add modified files
echo "Adding modified files..."
git add internal/provider/user_resource.go
git add internal/provider/user_resource_test.go
git add internal/provider/provider.go
git add metabase-api.yaml
git add metabase/metabase_response.go
git add metabase/client.gen.go

# Check if there's anything to commit
if git diff --cached --quiet; then
    echo "No changes to commit"
else
    # Create commit
    echo "Creating commit..."
    git commit -m "feat: add user resource support

- Add user resource with CRUD operations
- Add user API endpoints to metabase-api.yaml
- Add user response methods
- Add user resource tests
- Register user resource in provider

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
fi

# Push to GitHub
echo "Pushing to GitHub..."
git push -u origin main

echo ""
echo "✅ Successfully pushed to GitHub!"
echo ""
echo "Repository: https://github.com/occam-bci/terraform-provider-metabase"
