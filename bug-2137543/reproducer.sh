DEBIAN_FRONTEND=noninteractive apt-get install -y language-pack-de
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y snapd
export LANG=de_DE.UTF-8
snap install --edge test-snap-components-snapctl
snap run --shell test-snap-components-snapctl.test -c 'snapctl services'
