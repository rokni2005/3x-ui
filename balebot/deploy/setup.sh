#!/usr/bin/env bash
# Deploys/updates the fruit-order bot on a fresh Debian/Ubuntu-style Linux
# server. Safe to re-run: it pulls the latest code, rebuilds, and restarts
# the service, but never overwrites an existing /etc/balebot/balebot.env.
#
# Usage (as root):
#   ./setup.sh [git-ref]
#
# git-ref defaults to the claude/bale-fruit-order-bot-w2lyzu branch.
set -euo pipefail

REPO_URL="https://github.com/rokni2005/3x-ui.git"
GIT_REF="${1:-claude/bale-fruit-order-bot-w2lyzu}"
SRC_DIR="/opt/balebot/src"
BIN_DIR="/opt/balebot/bin"
DATA_DIR="/var/lib/balebot"
CONF_DIR="/etc/balebot"
ENV_FILE="$CONF_DIR/balebot.env"
GO_MIN_VERSION="1.21"
GO_INSTALL_VERSION="1.24.7"

if [[ $EUID -ne 0 ]]; then
	echo "Run this as root (e.g. sudo ./setup.sh)." >&2
	exit 1
fi

log() { echo "==> $*"; }

ensure_go() {
	if command -v go >/dev/null 2>&1; then
		local have
		have="$(go version | awk '{print $3}' | sed 's/go//')"
		log "found Go $have"
		return
	fi

	log "Go not found, installing Go $GO_INSTALL_VERSION to /usr/local/go"
	local arch
	case "$(uname -m)" in
		x86_64) arch=amd64 ;;
		aarch64) arch=arm64 ;;
		*) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
	esac
	local tarball="go${GO_INSTALL_VERSION}.linux-${arch}.tar.gz"
	curl -fsSL "https://go.dev/dl/${tarball}" -o "/tmp/${tarball}"
	rm -rf /usr/local/go
	tar -C /usr/local -xzf "/tmp/${tarball}"
	rm -f "/tmp/${tarball}"
	ln -sf /usr/local/go/bin/go /usr/local/bin/go
	ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
}

ensure_user() {
	if ! id balebot >/dev/null 2>&1; then
		log "creating system user 'balebot'"
		useradd --system --no-create-home --shell /usr/sbin/nologin balebot
	fi
}

ensure_dirs() {
	mkdir -p "$SRC_DIR" "$BIN_DIR" "$DATA_DIR/photos" "$CONF_DIR"
	chown -R balebot:balebot "$DATA_DIR"
	chmod 750 "$DATA_DIR"
}

fetch_source() {
	if [[ -d "$SRC_DIR/.git" ]]; then
		log "updating existing checkout"
		git -C "$SRC_DIR" fetch origin "$GIT_REF"
		git -C "$SRC_DIR" checkout "$GIT_REF"
		git -C "$SRC_DIR" reset --hard "origin/$GIT_REF"
	else
		log "cloning $REPO_URL ($GIT_REF)"
		git clone --branch "$GIT_REF" "$REPO_URL" "$SRC_DIR"
	fi
}

build_binary() {
	log "building balebot"
	( cd "$SRC_DIR/balebot" && go build -o "$BIN_DIR/balebot" . )
	chmod 755 "$BIN_DIR/balebot"
}

install_env_file() {
	if [[ -f "$ENV_FILE" ]]; then
		log "$ENV_FILE already exists, leaving it untouched"
		return 1
	fi
	log "creating $ENV_FILE from template -- EDIT THIS FILE before starting the service"
	install -m 600 -o root -g root "$SRC_DIR/balebot/deploy/balebot.env.example" "$ENV_FILE"
	return 0
}

install_unit() {
	log "installing systemd unit"
	install -m 644 "$SRC_DIR/balebot/deploy/balebot.service" /etc/systemd/system/balebot.service
	systemctl daemon-reload
}

main() {
	ensure_go
	ensure_user
	ensure_dirs
	fetch_source
	build_binary
	install_unit

	local first_setup=0
	if install_env_file; then
		first_setup=1
	fi

	if [[ "$first_setup" -eq 1 ]]; then
		cat <<EOF

==> First-time setup complete. Before starting the bot:
    1. Edit $ENV_FILE and fill in BALE_BOT_TOKEN, ADMIN_PASSWORD, etc.
    2. Start it:  systemctl enable --now balebot
    3. Watch logs: journalctl -u balebot -f
EOF
	else
		log "restarting balebot to pick up the new build"
		systemctl restart balebot
		systemctl --no-pager status balebot || true
	fi
}

main "$@"
