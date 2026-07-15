#!/bin/bash
set -e

# Create panmail user if it doesn't exist
if ! id "panmail" &>/dev/null; then
    useradd --system --user-group --home-dir /var/lib/panmail --create-home panmail
fi

# Ensure config directory exists
mkdir -p /etc/panmail
chown -R panmail:panmail /etc/panmail
chmod 755 /etc/panmail

# Ensure log directory exists
mkdir -p /var/log/panmail
chown -R panmail:panmail /var/log/panmail
chmod 755 /var/log/panmail

# Ensure working directory exists
mkdir -p /var/lib/panmail
chown -R panmail:panmail /var/lib/panmail
chmod 755 /var/lib/panmail

# Reload systemd
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
fi

exit 0
