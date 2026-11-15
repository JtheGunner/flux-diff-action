<!-- flux-diff -->
## flux-diff: manifest changes

**1 added, 1 changed, 1 removed** (1 more suppressed as noise)

Result is incomplete: see the section on units that were not fully rendered.

### Not rendered / warnings

- **SOPS (partial):** ` [SOPSUnreadable] orig snapshot: Secret demo/demo-credentials: SOPS-encrypted data is unreadable; supply dummy values for references; re-encryption cannot be compared (keys: password, api-token) `
- **External source (not rendered):** ` [ExternalSource] current snapshot: Kustomization flux-system/demo-crds: external source GitRepository.source.toolkit.fluxcd.io/flux-system/demo-upstream is outside the local tree; not rendered `
- **Warning:** ` [UnrelatedFailure] current snapshot: OCIRepository flux-system/demo-charts: machine credential acquisition failed `

### ` HelmRelease demo/demo-app `

<details>
<summary>changed <code>ConfigMap demo/demo-app</code></summary>

```diff
@@ -1,7 +1,7 @@
 {
   "apiVersion": "v1",
   "data": {
-    "token": "FAKE-DO-NOT-USE-old"
+    "token": "FAKE-DO-NOT-USE-new"
   },
   "kind": "ConfigMap",
   "metadata": {
```
</details>

### Other objects

<details>
<summary>added <code>Namespace demo-extra</code></summary>

```diff
@@ -0,0 +1,7 @@
+{
+  "apiVersion": "v1",
+  "kind": "Namespace",
+  "metadata": {
+    "name": "demo-extra"
+  }
+}
```
</details>

<details>
<summary>removed <code>Deployment demo/demo-old</code></summary>

```diff
@@ -1,8 +0,0 @@
-{
-  "apiVersion": "apps/v1",
-  "kind": "Deployment",
-  "metadata": {
-    "name": "demo-old",
-    "namespace": "demo"
-  }
-}
```
</details>

