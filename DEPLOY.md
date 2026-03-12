# GoCognigo — Cloud Run Deployment Guide

## Prerequisites

- [Google Cloud CLI](https://cloud.google.com/sdk/docs/install) (`gcloud`)
- A Google Cloud project with billing enabled
- Docker (for local testing, optional)

## 1. Firebase Auth Setup

1. Go to [Firebase Console](https://console.firebase.google.com/) and create a project (or use your GCP project)
2. Enable **Authentication** → Sign-in method → enable **Google** and/or **Email/Password**
3. Go to **Project Settings** → **General** → scroll to "Your apps" → **Add web app**
4. Copy the Firebase config values into your `.env` file:

```env
FIREBASE_PROJECT_ID=your-project-id
FIREBASE_API_KEY=AIza...
FIREBASE_AUTH_DOMAIN=your-project-id.firebaseapp.com
FIREBASE_STORAGE_BUCKET=your-project-id.appspot.com
FIREBASE_MESSAGING_SENDER_ID=123456789
FIREBASE_APP_ID=1:123456789:web:abc123
```

## 2. Deploy to Cloud Run

```bash
# Set your project
export PROJECT_ID=your-project-id
export REGION=us-central1

gcloud config set project $PROJECT_ID

# Enable required APIs
gcloud services enable run.googleapis.com artifactregistry.googleapis.com cloudbuild.googleapis.com

# Build and deploy in one step
gcloud run deploy gocognigo \
  --source . \
  --region $REGION \
  --allow-unauthenticated \
  --memory 512Mi \
  --cpu 1 \
  --min-instances 0 \
  --max-instances 3 \
  --set-env-vars "FIREBASE_PROJECT_ID=$FIREBASE_PROJECT_ID" \
  --set-env-vars "FIREBASE_API_KEY=$FIREBASE_API_KEY" \
  --set-env-vars "FIREBASE_AUTH_DOMAIN=$FIREBASE_AUTH_DOMAIN" \
  --set-env-vars "FIREBASE_STORAGE_BUCKET=$FIREBASE_STORAGE_BUCKET" \
  --set-env-vars "FIREBASE_MESSAGING_SENDER_ID=$FIREBASE_MESSAGING_SENDER_ID" \
  --set-env-vars "FIREBASE_APP_ID=$FIREBASE_APP_ID"
```

> **Note:** LLM API keys (OPENAI_API_KEY, ANTHROPIC_API_KEY, etc.) are configured per-user
> in the app's Settings panel, so they don't need to be set as environment variables.

## 3. Persistent Storage

Cloud Run containers are stateless. For persistent data (projects, indexes), attach a
Cloud Storage FUSE volume or use a persistent disk:

```bash
# Create a Cloud Storage bucket for data
gsutil mb -l $REGION gs://${PROJECT_ID}-gocognigo-data

# Redeploy with volume mount
gcloud run deploy gocognigo \
  --source . \
  --region $REGION \
  --allow-unauthenticated \
  --memory 512Mi \
  --execution-environment gen2 \
  --add-volume=name=data-vol,type=cloud-storage,bucket=${PROJECT_ID}-gocognigo-data \
  --add-volume-mount=volume=data-vol,mount-path=/app/data
```

## 4. Custom Domain / Subdomain

### Option A: Cloud Run domain mapping (simplest)

```bash
# Map your subdomain to the Cloud Run service
gcloud run domain-mappings create \
  --service gocognigo \
  --domain cognigo.yourdomain.com \
  --region $REGION
```

Then add the DNS records shown in the output to your domain registrar:
- Usually a CNAME record pointing to `ghs.googlehosted.com`

### Option B: Cloudflare proxy (if using Cloudflare)

1. Get your Cloud Run service URL:
   ```bash
   gcloud run services describe gocognigo --region $REGION --format='value(status.url)'
   ```
2. In Cloudflare DNS, add a CNAME record:
   - Name: `cognigo` (or your chosen subdomain)
   - Target: your Cloud Run URL (without `https://`)
   - Proxy: enabled (orange cloud)

### Option C: Load Balancer with SSL (production)

For more control, set up a Google Cloud Load Balancer with a managed SSL certificate:

```bash
# Create a serverless NEG
gcloud compute network-endpoint-groups create gocognigo-neg \
  --region=$REGION \
  --network-endpoint-type=serverless \
  --cloud-run-service=gocognigo

# Create backend service, URL map, SSL cert, and forwarding rule
# See: https://cloud.google.com/run/docs/mapping-custom-domains#https-load-balancer
```

## 5. Firebase Auth Domain Configuration

After mapping your custom domain, add it to Firebase's authorized domains:

1. Firebase Console → Authentication → Settings → Authorized domains
2. Add your custom domain (e.g., `cognigo.yourdomain.com`)

This is required for Google Sign-In to work on your custom domain.

## Free Tier Notes

Google Cloud Run free tier includes:
- 2 million requests/month
- 360,000 GB-seconds of memory
- 180,000 vCPU-seconds

Firebase Auth free tier (Spark plan):
- Unlimited email/password sign-ins
- Unlimited Google sign-ins

This should be more than sufficient for a personal or small community deployment.
