# Apifox Import and Debug Guide

## 1. Import

1. Open Apifox.
2. Create a new project (or choose an existing one).
3. Import -> OpenAPI/Swagger.
4. Select file: `docs/apifox/openapi.yaml`.
5. Import as new API collection.

## 2. Recommended Environment Variables

Create an environment (for example `local`) and define:

- `baseUrl`: `http://localhost:8080`
- `accessToken`: empty initially
- `refreshToken`: empty initially
- `projectId`: fill after creating a project
- `versionId`: fill after creating a version
- `memberId`: fill after listing members
- `docId`: fill after uploading a document
- `taskId`: fill when you have an AI task record

## 3. Global Auth Setup in Apifox

In project settings or folder-level auth:

- Auth type: `Bearer Token`
- Token value: `{{accessToken}}`

This allows all protected APIs to use your login token automatically.

## 4. Login Script (Auto Save Token)

Add this script to `POST /api/v1/auth/login` as a post-response script.

```javascript
let body = {};
try {
  body = pm.response.json();
} catch (e) {}

const data = body && body.data ? body.data : {};
if (data.accessToken) {
  pm.environment.set('accessToken', data.accessToken);
}
if (data.refreshToken) {
  pm.environment.set('refreshToken', data.refreshToken);
}
```

Add this script to `POST /api/v1/projects` post-response script to auto save project ID:

```javascript
let body = {};
try {
  body = pm.response.json();
} catch (e) {}

const p = body && body.data && body.data.project ? body.data.project : null;
if (p && p.id) {
  pm.environment.set('projectId', p.id);
}
```

Add this script to `POST /api/v1/projects/{{projectId}}/versions` post-response script:

```javascript
let body = {};
try {
  body = pm.response.json();
} catch (e) {}

const v = body && body.data && body.data.version ? body.data.version : null;
if (v && v.id) {
  pm.environment.set('versionId', v.id);
}
```

## 5. Quick End-to-End Debug Flow

1. `POST /api/v1/auth/register`
2. `POST /api/v1/auth/login` (token auto-saved)
3. `GET /api/v1/users/me`
4. `POST /api/v1/projects` (projectId auto-saved)
5. `GET /api/v1/projects`
6. `GET /api/v1/fault-trees/{{projectId}}/graph`
7. `POST /api/v1/fault-trees/{{projectId}}/graph/save`
8. `POST /api/v1/projects/{{projectId}}/versions`
9. `GET /api/v1/projects/{{projectId}}/versions`
10. `POST /api/v1/documents/upload`

## 6. Important Runtime Notes

- Protected APIs require header `Authorization: Bearer <accessToken>`.
- Backend returns most business failures as HTTP 200 with `code != 0`.
- Missing/invalid token may return HTTP 401 directly.
- Pagination defaults: `page=1`, `pageSize=20`, max `pageSize=100`.
- Project list sort fields currently use DB field names:
  - `created_at`
  - `updated_at`
  - `name`
- Upload fields:
  - Avatar API uses form field `avatar`.
  - Document API uses form field `files` (multi-file array).

## 7. Scope of Current Implementation

The generated OpenAPI file only covers APIs currently wired in `cmd/server/main.go` and implemented handlers.
Not-yet-implemented capability in requirement docs (for example OAuth login, AI generation endpoints, team overview endpoints, notifications) is intentionally excluded to avoid import-time noise and debug failures.
