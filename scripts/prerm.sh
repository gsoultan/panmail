#!/bin/bash
set -e

# Stop and disable service before removal
if [ -d /run/systemd/system ]; then
    systemctl stop panmail || true
    systemctl disable panmail || true
fi

exit 0
