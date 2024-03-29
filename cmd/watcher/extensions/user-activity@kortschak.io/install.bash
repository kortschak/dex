#!/usr/bin/env bash

cd $(dirname "$0")
name=user-activity-kortschak.io.v$(jq -c '.version' <metadata.json).shell-extension.zip
zip ${name} extension.js metadata.json
gnome-extensions install ${name}

echo 'Please restart your GNOME session and run `gnome-extensions enable user-activity-kortschak.io` to make the extension available.'
