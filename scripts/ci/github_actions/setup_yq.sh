#!/bin/bash -xe
set +o pipefail

cat >>/home/runner/.bash_profile <<'EOL'
yq() {
  docker run --rm -i -v "${PWD}":/workdir mikefarah/yq "$@"
}
EOL
