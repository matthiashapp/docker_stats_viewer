#!/bin/bash
TARGET_SERVER="t3" # ssh ref
DATE=$(date +%Y-%m-%d_%H-%M-%S)
OUTPUT_FILE="${DATE}_docker_stats.json"
ssh "$TARGET_SERVER" "docker stats --no-stream --format '{{json .}}' > $OUTPUT_FILE" &&
scp "$TARGET_SERVER:$OUTPUT_FILE" stats/ &&
echo "Docker stats saved to $OUTPUT_FILE" || echo "Failed to retrieve Docker stats from $TARGET_SERVER"

#docker stats --no-stream --format "{{json .}}" > docker_stats.json
