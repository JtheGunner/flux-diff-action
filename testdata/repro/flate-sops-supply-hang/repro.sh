#!/usr/bin/env bash
# Builds a small two-commit Flux repository that makes `flate diff` spin at
# ~200% CPU, then runs flate on it with a time limit.
#
# Usage: repro.sh <target-dir> [flate-binary] [timeout-seconds]
#
# The target directory must not exist yet. All Secret values are fake.
# See README.md in this directory for what the repository demonstrates.
set -euo pipefail

if [ "$#" -lt 1 ] || [ "$#" -gt 3 ]; then
  echo "usage: $0 <target-dir> [flate-binary] [timeout-seconds]" >&2
  exit 2
fi

target=$1
flate=${2:-flate}
limit=${3:-30}

if [ -e "$target" ]; then
  echo "error: $target already exists" >&2
  exit 2
fi

mkdir -p "$target/sources" "$target/clusters" "$target/apps" "$target/velero"
cd "$target"

git init -q -b main
git config user.email repro@example.invalid
git config user.name repro

cat > sources/vmware-tanzu.yaml <<'YAML'
apiVersion: source.toolkit.fluxcd.io/v1
kind: HelmRepository
metadata:
  name: vmware-tanzu
  namespace: flux-system
spec:
  interval: 1h
  url: https://vmware-tanzu.github.io/helm-charts
YAML

cat > clusters/apps.yaml <<'YAML'
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: apps
  namespace: flux-system
spec:
  interval: 5m
  path: ./apps
  prune: true
  sourceRef:
    kind: GitRepository
    name: flux-system
YAML

cat > apps/kustomization.yaml <<'YAML'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - velero.yaml
YAML

cat > apps/velero.yaml <<'YAML'
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: velero
  namespace: flux-system
spec:
  interval: 1h
  path: ./velero
  prune: true
  sourceRef:
    kind: GitRepository
    name: flux-system
YAML

cat > velero/namespace.yaml <<'YAML'
apiVersion: v1
kind: Namespace
metadata:
  name: velero
YAML

cat > velero/secret.sops.yaml <<'YAML'
# FAKE-DO-NOT-USE: not a real SOPS payload, only the shape of one.
apiVersion: v1
kind: Secret
metadata:
  name: velero-s3-credentials
  namespace: velero
type: Opaque
stringData:
  cloud: ENC[AES256_GCM,data:RkFLRS1ETy1OT1QtVVNF,iv:RkFLRS1ETy1OT1QtVVNFRkFLRS1ETy1OT1Q=,tag:RkFLRS1ETy1OT1QtVVNFRg==,type:str]
sops:
  age:
    - recipient: age1fakefakefakefakefakefakefakefakefakefakefakefakefakefakefake
      enc: |
        -----BEGIN AGE ENCRYPTED FILE-----
        RkFLRS1ETy1OT1QtVVNF
        -----END AGE ENCRYPTED FILE-----
  lastmodified: "2026-01-01T00:00:00Z"
  mac: ENC[AES256_GCM,data:RkFLRS1ETy1OT1QtVVNF,iv:RkFLRS1ETy1OT1QtVVNF,tag:RkFLRS1ETy1OT1QtVVNF,type:str]
  encrypted_regex: ^(data|stringData)$
  version: 3.10.2
YAML

cat > velero/helmrelease.yaml <<'YAML'
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: velero
  namespace: velero
spec:
  interval: 1h
  chart:
    spec:
      chart: velero
      version: "12.1.0"
      sourceRef:
        kind: HelmRepository
        name: vmware-tanzu
        namespace: flux-system
  valuesFrom:
    - kind: Secret
      name: velero-s3-credentials
      valuesKey: cloud
      targetPath: credentials.secretContents.cloud
  values:
    snapshotsEnabled: true
    configuration:
      features: EnableCSI
      backupStorageLocation:
        - name: default
          provider: aws
          bucket: example-bucket
          config:
            region: example
            s3Url: http://minio.example.invalid:9000
      volumeSnapshotLocation:
        - name: default
          provider: csi
    credentials:
      useSecret: true
    initContainers:
      - name: velero-plugin-for-aws
        image: velero/velero-plugin-for-aws:v1.13.0
        volumeMounts:
          - mountPath: /target
            name: plugins
YAML

cat > velero/kustomization.yaml <<'YAML'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - namespace.yaml
  - helmrelease.yaml
  - secret.sops.yaml
YAML

cat > supply.yaml <<'YAML'
# Stand-in for the SOPS-encrypted Secret in velero/, passed with --supply.
apiVersion: v1
kind: Secret
metadata:
  name: velero-s3-credentials
  namespace: velero
stringData:
  cloud: ""
YAML

git add -A
git commit -qm "base"

# The head commit changes one Helm value, so the rendered HelmRelease differs.
sed -i.bak 's/snapshotsEnabled: true/snapshotsEnabled: false/' velero/helmrelease.yaml
rm velero/helmrelease.yaml.bak
git commit -qam "head: disable snapshots in the Velero values"

echo "Repository created in $target"
echo "Running: $flate diff --base HEAD~1 --supply supply.yaml (limit ${limit}s)"

"$flate" diff -o json --no-progress --base HEAD~1 --supply supply.yaml \
  --skip-kinds Secret,CustomResourceDefinition >/dev/null 2>&1 &
pid=$!

for _ in $(seq 1 "$limit"); do
  if ! kill -0 "$pid" 2>/dev/null; then
    status=0
    wait "$pid" || status=$?
    echo "flate finished with exit code $status: the hang did not reproduce"
    exit 0
  fi
  sleep 1
done

kill "$pid" 2>/dev/null || true
wait "$pid" 2>/dev/null || true
echo "flate was still running after ${limit}s and was stopped: the hang reproduced"
