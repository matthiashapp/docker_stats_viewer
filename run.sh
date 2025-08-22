#!/bin/bash
DATE=$(date +%Y-%m-%d_%H-%M-%S)
OUTPUT_FILE="stats/${DATE}_docker_stats.json"

docker stats --no-stream --format '{{json .}}' > "$OUTPUT_FILE"
if [ $? -eq 0 ]; then
	echo "Docker stats saved to $OUTPUT_FILE"
else
	echo "Failed to retrieve Docker stats on local machine"
fi
