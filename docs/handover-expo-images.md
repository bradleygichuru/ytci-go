# Expo Mobile — Image URL Handover

## Overview

The Go backend now returns `objectKey` fields in mobile API responses for Stories, Destinations, and Challenge Evidence. The mobile app resolves these to usable image URLs by calling the media endpoint.

## Media Resolution Pattern

All images use Cloudflare R2 storage. The backend stores **object keys** (e.g. `media/1712345678/photo.jpg`) in the database. The mobile app must **never** construct R2 URLs directly — it must resolve object keys via this endpoint:

```
GET /v1/mobile/media/{objectKey}

Response:
{
  "url": "https://<r2-bucket>.r2.cloudflarestorage.com/media/...?X-Amz-Signature=...",
  "expiresAt": "2026-07-30T09:15:00Z"
}
```

The returned URL is a **presigned GET URL** valid for 15 minutes. Cache it client-side and re-fetch when expired.

## Updated Endpoints

### Stories

**`GET /v1/mobile/stories`** — `objectKey` → `GET /v1/mobile/media/{objectKey}` returns presigned URL.
**`GET /v1/mobile/stories/{id}`** — Same pattern for each item in `media` array.
**`GET /v1/mobile/stories/mine`** — Same pattern. 

**`thumbnailKey`** — Optional. When present, use this for list thumbnails and the `objectKey` for detail views. 

### Destinations

**`GET /v1/mobile/destinations`**, `GET /v1/mobile/destinations/{slug}`, `GET /v1/mobile/destinations/nearby` — Each response now includes a `media` array with `objectKey`, `thumbnailKey`, `type`, and `altText`. The first item is typically the hero image. Subsequent items are gallery images. No rendering — resolve each `objectKey` individually.

### Challenge Evidence

**`POST /v1/mobile/challenges/{id}/evidence`** — `mediaIds` from local state as a comma-separated string of media asset IDs sent by the Expo app. The backend now stores them in the evidence JSONB alongside `description`.

## Example: Rendering a Story Card

```ts
// 1. Fetch story list
const res = await fetch("/v1/mobile/stories")
const { items } = await res.json()

// 2. For each story, resolve thumbnail URLs
for (const story of items) {
  const thumbObjKey = story.media?.[0]?.thumbnailKey ?? story.media?.[0]?.objectKey
  if (thumbObjKey) {
    const mediaRes = await fetch(`/v1/mobile/media/${thumbObjKey}`)
    const { url, expiresAt } = await mediaRes.json()
    story.thumbnailUrl = url       // cache this, re-fetch before expiry
    story.thumbnailExpires = expiresAt
  }
}
```

## API Changes Summary

| Endpoint | New field | Type | Description |
|----------|-----------|------|------------|
| `GET /v1/mobile/stories` | `media` | `MediaItem[]` | Array of media objects per story |
| `GET /v1/mobile/stories/{id}` | `media` | `MediaItem[]` | Same |
| `GET /v1/mobile/stories/mine` | `media` | `MediaItem[]` | Same |
| `GET /v1/mobile/destinations` | `media` | `MediaItem[]` | Hero (index 0) + gallery (rest) |
| `GET /v1/mobile/destinations/{slug}` | `media` | `MediaItem[]` | Same |
| `GET /v1/mobile/destinations/nearby` | `media` | `MediaItem[]` | Same + `distanceMeters` |
| `POST /v1/mobile/challenges/{id}/evidence` | `mediaIds` (req) | string | Comma-separated media IDs stored in evidence |

### MediaItem shape

```json
{
  "objectKey": "media/1712345678/photo.jpg",
  "thumbnailKey": "media/1712345678/thumb.jpg",
  "type": "image",
  "altText": "A lion in the savanna"
}
```

### `GET /v1/mobile/stories/{id}` Response Example

```json
{
  "id": "uuid",
  "caption": "What an amazing safari!",
  "media": [
    {
      "objectKey": "media/1712345678/photo.jpg",
      "thumbnailKey": "media/1712345678/thumb.jpg",
      "type": "image",
      "altText": "Lion at sunset"
    }
  ],
  "likeCount": 12,
  "saveCount": 3,
  "createdAt": "2026-07-29T10:30:00Z",
  "isLiked": false,
  "isSaved": false
}
```

## Important Notes

1. **Never concatenate R2 URLs manually.** Always use the `/mobile/media/{objectKey}` endpoint. Presigned URLs contain short-lived signing tokens.
2. **Cache presigned URLs client-side** for their 15-minute lifetime to avoid unnecessary network calls. Re-fetch before expiry.
3. **Empty `media` arrays** are returned as `[]` when no media is linked. Handle gracefully (show placeholder).
4. **Destination responses now use camelCase** JSON keys (`shortDescription` not `short_description`). Update any existing code that references snake_case keys.
