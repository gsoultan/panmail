#!/bin/bash
set -e

# Reload systemd after removal
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
fi

exit 0
