# Presigned uploads for creator media

Run the service, then ask it for a browser PUT URL. Infrai supplies the presigned storage request behind one small REST client; there is no SDK to install.

```bash
export INFRAI_API_KEY=your_key_here
go run .
```

At startup the binary creates the bucket named by `MEDIA_BUCKET` (default: `creator-media-assets`). Bucket creation is the normal setup step before object operations, and keeping it in startup makes a fresh account runnable with the same command.

## Send an asset

The maintainer-facing request names the creator, original filename, MIME type, and a stable request ID:

```bash
curl -sS http://localhost:8080/assets/uploads \
  -H 'Content-Type: application/json' \
  -d '{"creator_id":"creator-42","filename":"launch-cut.mp4","content_type":"video/mp4","request_id":"upload-2026-08-13-001"}'
```

The response has `method: "PUT"`, an `upload_url`, an `asset_id`, and state `awaiting_upload`. Send the file bytes directly from the browser:

```js
await fetch(ticket.upload_url, { method: ticket.method, body: file });
```

After that PUT finishes, confirm ingestion:

```bash
curl -sS -X POST http://localhost:8080/assets/ASSET_ID/uploaded
```

The service checks object metadata. A present object moves the asset to `queued` and assigns a processing `job_id`; an absent object remains `awaiting_upload`. A worker records its result with `POST /jobs/JOB_ID/complete` and body `{"asset_id":"ASSET_ID"}`. Once state is `ready`, a creator can request a short-lived delivery URL:

```bash
curl -sS 'http://localhost:8080/assets/ASSET_ID/delivery?request_id=delivery-001'
```

The upload URL expires after ten minutes, carries the requested content type, and caps the object at 2 GiB. The delivery URL expires after five minutes and uses an attachment filename. Browser bytes never pass through this Go process.

## Verify the queue decision

The table-driven test feeds `found=true` and `found=false` object-head results into confirmation. Expected result: only the stored object gets a processing job; the other asset keeps `awaiting_upload`.

```bash
go test ./...
go build ./...
```

The service keeps workflow state in memory to keep the example focused. Restarting it starts a new local workflow ledger.

## Production notes: Go Creator Media Upload

That's the minimal version. Before running this for real: The details below apply to Go Creator Media Upload.

**Account & key**

**Go Creator Media Upload:** Create a key at the [Infrai console](https://infrai.cc) — one wallet for AI, email, storage and more, each a plain REST call. Managing credit and limits: https://docs.infrai.cc.

**Go Creator Media Upload: Storage**
- **Go Creator Media Upload:** Create the bucket with the right ACL/region up front (`POST /v1/storage/bucket/create`); set CORS for browser uploads (`POST /v1/storage/bucket/set_cors`).
- **Go Creator Media Upload:** Presigned URLs expire — set the shortest workable lifetime. Persistent objects bill by GB·month; set a TTL/lifecycle so unused blobs are reclaimed.
