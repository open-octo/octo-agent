---
name: deploy
description: Deploy Rails applications to Railway. Handles first-time setup and re-deploys idempotently using Railway CLI. Trigger on: "deploy", "deploy to railway", "railway deploy", "发布", "部署", "上线".
user-invocable: true
---

# Deploy Rails App to Railway

Deploy the current Rails project to Railway using the Railway CLI. Works for both first-time deploys and re-deploys.

## Prerequisites Check

Before starting, verify:

```bash
# 1. Railway CLI installed?
railway --version

# 2. Logged in?
railway whoami
```

If not logged in, instruct the user:
```
Please run: railway login
Then retry deployment.
```

## Step 0: Prepare for Linux Build

Railway runs on Linux. Ensure Gemfile.lock includes the linux platform:

```bash
bundle lock --add-platform x86_64-linux
```

If the project uses a `Dockerfile` builder (check `railway.toml` for `builder = "DOCKERFILE"`), no `Procfile` is needed — skip creating one.

Only create a `Procfile` if there is no Dockerfile:
```
web: bundle exec puma -C config/puma.rb
```

## Step 1: Check Link Status → Deploy Immediately if Already Linked

**First: check if already linked:**
```bash
railway status 2>&1
```

**If output contains `Project:` → project is already linked.**
Skip Steps 2–5 entirely and jump to Step 6 (Deploy).

**If output contains "not linked" or an error → not linked yet.**
Try linking to an existing project first — list available projects:
```bash
railway list 2>&1 | grep -i "<app-name>"
```

If a matching project is found, link it:
```bash
railway link --project <project-name> --service <service-name> 2>&1
```

Only if no existing project is found, init a new one:
```bash
railway init -n <app-name>
```
The `app-name` should match the current directory name (e.g., `my-rails-app`).

**⚠️ NEVER run `railway init` when already linked or when an existing project exists.**
It silently creates a brand-new Railway project. If this happens by mistake:
1. Find the correct project name from `railway list`
2. Re-link: `railway link --project <project-name> --service <service-name>`

## Step 2: Set Environment Variables

Set required Rails production variables. Use `--skip-deploys` to avoid triggering premature deploys:

```bash
# Generate a secret key base
SECRET_KEY_BASE=$(bundle exec rails secret)

railway variable set SECRET_KEY_BASE=$SECRET_KEY_BASE --skip-deploys
railway variable set RAILS_ENV=production --skip-deploys
railway variable set RAILS_LOG_TO_STDOUT=true --skip-deploys
railway variable set RAILS_SERVE_STATIC_FILES=true --skip-deploys
```

If the project uses any other env vars (check `.env.example` or `config/application.yml.example` if they exist), prompt the user to provide values and set them too.

If the project uses `config/application.yml` (Figaro gem), read it and set all values as Railway variables:
```bash
# Read application.yml and set each key=value pair
ruby -ryaml -e "
  data = YAML.safe_load(File.read('config/application.yml')) || {}
  data.each { |k, v| puts %(railway variable set #{k}=#{v} --skip-deploys) unless v.to_s.empty? }
" | bash
```

## Step 3: Ensure PostgreSQL Service (Idempotent)

Check if Postgres already exists:

```bash
railway status --json
```

Parse the JSON output. If a service with type `postgres` or name containing `postgres`/`Postgres` is already found, skip with: `✅ PostgreSQL already provisioned`

**⚠️ IMPORTANT: `railway add --database postgres` has a known CLI bug that always returns `Unauthorized`.**
Do NOT attempt to run this command. Instead, instruct the user to add PostgreSQL manually via the Railway Web UI:

1. Open your Railway project: `https://railway.com/project/<project-id>`
   (Get the project ID from the Railway dashboard or `cat .railway/config.json`)
2. Click **"+ New"** → **"Database"** → **"PostgreSQL"**
3. Wait for the database to provision
4. Come back and continue

After Postgres is provisioned, set the DATABASE_URL variable:
```bash
railway variable set DATABASE_URL='${{Postgres.DATABASE_URL}}' --skip-deploys
```

## Step 4: Get Domain (Idempotent)

Check if a domain is already set:

```bash
railway domain --json
```

If no domain exists yet:
```bash
railway domain
```

Capture and display the domain URL to the user. Also set it as PUBLIC_HOST:
```bash
railway variable set PUBLIC_HOST=<domain-without-https> --skip-deploys
```

## Step 5: Configure Storage Bucket (if needed)

Check if the project uses S3-compatible storage by reading `config/storage.yml`. If it contains an `amazon` or `s3` service section, storage bucket configuration is required.

**⚠️ Storage bucket requires Railway Hobby plan ($5/month minimum). Confirm with the user before proceeding.**

Check if bucket env vars are already set:
```bash
railway variables --json | grep STORAGE_BUCKET
```

If not set, create a bucket and configure the variables:

```bash
# Create bucket (choose region: iad=US East, sjc=US West, ams=EU, sin=Asia)
railway bucket create <app-name>-storage --region iad --json

# Get credentials
railway bucket credentials --bucket <app-name>-storage --json
```

The credentials JSON will contain: `accessKeyId`, `secretAccessKey`, `region`, `endpoint`, `bucketName`.

Set them as environment variables:
```bash
railway variables set \
  STORAGE_BUCKET_ACCESS_KEY_ID=<accessKeyId> \
  STORAGE_BUCKET_SECRET_ACCESS_KEY=<secretAccessKey> \
  STORAGE_BUCKET_REGION=<region> \
  STORAGE_BUCKET_NAME=<bucketName> \
  STORAGE_BUCKET_ENDPOINT=<endpoint> \
  --skip-deploys
```

**⚠️ Missing these variables will cause a hard crash at boot (`Aws::Errors::MissingRegionError`) because the AWS SDK initializes at startup, not lazily.**

If the project does not use S3 storage, skip this step entirely.

## Step 6: Deploy

Upload and deploy the project:

```bash
railway up --detach
```

Show the user the deployment is in progress and they can monitor it with:
```bash
railway logs
```

**No manual migration needed.** The `bin/docker-entrypoint` script runs `rails db:prepare` automatically on container startup. Just wait for the deployment to complete.

## Step 7: Verify Deployment

After deployment completes (wait ~30 seconds), verify the app is running:

```bash
# Should return 200
curl -s -o /dev/null -w "%{http_code}" https://<domain>/
```

If it returns `200`, deployment is successful. If not, check logs:
```bash
railway logs --tail 50
```

## Step 8: Done

Print a summary:
```
✅ Deployment complete!
🌐 Platform URL: https://<domain>
📋 Monitor: railway logs
🔄 Re-deploy: just run deploy again
```

---

## Notes

- **Idempotency**: Running this skill multiple times is safe. Each step checks current state before acting.
- **Link detection**: Use `railway status` to check if already linked — it's more reliable than checking `.railway/config.json` (works across machines and fresh clones).
- **Re-deploy**: On subsequent runs, Steps 1–5 are all skipped or no-ops. Only Step 6 (upload) actually runs.
- **Secret key**: Only set `SECRET_KEY_BASE` if not already set (check with `railway variable list`).
- **Database migrations**: Handled automatically by `bin/docker-entrypoint` via `rails db:prepare` — never run `railway run bundle exec rails db:migrate` as Railway's internal DB IP is not accessible from local machine.
- **PostgreSQL CLI bug**: `railway add --database postgres` always fails with `Unauthorized` — always use the Web UI instead.
