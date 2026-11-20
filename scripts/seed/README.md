# Species seed — Big Five

Internal-test content for the species surface.

1. Upload the hero images (requires the R2 credentials from the Render service):
   ```sh
   R2_ACCOUNT_ID=... R2_ACCESS_KEY_ID=... R2_SECRET_ACCESS_KEY=... R2_BUCKET=ytci \
     scripts/seed/r2-upload-big-five.sh
   ```
   This writes `species/big-five/<slug>.jpg` for lion, African elephant, buffalo,
   leopard and black rhinoceros. Images are Wikimedia Commons lead images; the
   SQL records the credit and licence.
2. Apply the content:
   ```sh
   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/seed/big-five.sql
   ```
   Idempotent by slug/name/content. Publishes all five with EN + SW names, prose,
   a reviewed hero image and where-to-see entries.
