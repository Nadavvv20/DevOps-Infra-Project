#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Purge Deleted Secrets Manager Secrets
#
# Lists and force-deletes secrets in "pending deletion" (scheduled for deletion)
# state matching a given project/cluster prefix.
#
# Usage:
#   ./scripts/purge-deleted-secrets.sh [--region <region>] [--prefix <prefix>] [--yes]
#
# Defaults:
#   region: il-central-1 (or $AWS_REGION)
#   prefix: online-boutique (or $CLUSTER_NAME)
#   mode:   dry-run by default; requires --yes to execute deletion
# ─────────────────────────────────────────────────────────────────────────────

set -eo pipefail

REGION="${AWS_REGION:-il-central-1}"
PREFIX="${CLUSTER_NAME:-online-boutique}"
CONFIRM=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --region)
      REGION="$2"
      shift 2
      ;;
    --prefix)
      PREFIX="$2"
      shift 2
      ;;
    --yes|-y)
      CONFIRM=true
      shift
      ;;
    -h|--help)
      echo "Usage: $0 [--region <aws-region>] [--prefix <secret-prefix>] [--yes]"
      echo ""
      echo "Options:"
      echo "  --region <region>   AWS region (default: il-central-1 or \$AWS_REGION)"
      echo "  --prefix <prefix>   Secret name prefix filter (default: online-boutique)"
      echo "  --yes, -y           Execute force deletion without recovery (dry-run if omitted)"
      exit 0
      ;;
    *)
      echo "Unknown argument: $1"
      exit 1
      ;;
  esac
done

echo "=========================================================="
echo " Secrets Manager Pending-Deletion Purge Tool"
echo " Region: ${REGION}"
echo " Prefix: ${PREFIX}"
echo " Mode:   $([ "$CONFIRM" = true ] && echo "EXECUTE (--yes)" || echo "DRY-RUN (safe)")"
echo "=========================================================="

# Query secrets pending deletion starting with prefix
DELETED_SECRETS=$(aws secretsmanager list-secrets \
  --include-planned-deletion \
  --region "${REGION}" \
  --query "SecretList[?DeletedDate!=null && starts_with(Name, '${PREFIX}')].[Name, to_string(DeletionDate)]" \
  --output text)

if [ -z "$DELETED_SECRETS" ] || [ "$DELETED_SECRETS" = "None" ]; then
  echo "No secrets pending deletion found matching prefix '${PREFIX}' in region '${REGION}'."
  exit 0
fi

echo "Secrets scheduled for deletion:"
echo "----------------------------------------------------------"
printf "%-50s %s\n" "SECRET NAME" "DELETION DATE"
echo "----------------------------------------------------------"
while IFS=$'\t' read -r name del_date; do
  [ -z "$name" ] && continue
  printf "%-50s %s\n" "$name" "$del_date"
done <<< "$DELETED_SECRETS"
echo "----------------------------------------------------------"

SECRET_NAMES=$(echo "$DELETED_SECRETS" | awk '{print $1}')
COUNT=$(echo "$SECRET_NAMES" | grep -c . || true)

if [ "$CONFIRM" != true ]; then
  echo ""
  echo "[DRY-RUN] Found ${COUNT} secret(s) scheduled for deletion."
  echo "To permanently force-delete these secrets now, re-run with --yes:"
  echo "  $0 --region \"${REGION}\" --prefix \"${PREFIX}\" --yes"
  exit 0
fi

echo ""
echo "Purging ${COUNT} secret(s) with --force-delete-without-recovery..."
while read -r name; do
  [ -z "$name" ] && continue
  echo "Force-deleting: $name"
  aws secretsmanager delete-secret \
    --secret-id "$name" \
    --force-delete-without-recovery \
    --region "${REGION}" >/dev/null
  echo "  ✓ Permanently deleted: $name"
done <<< "$SECRET_NAMES"

echo ""
echo "Successfully purged ${COUNT} secret(s) in region '${REGION}'."
