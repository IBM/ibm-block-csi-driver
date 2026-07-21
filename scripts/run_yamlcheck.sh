#!/bin/bash -e
set -x

arch=$(uname -m)
if [[ "$arch" == "x86"* ]];
then
  KUBECONFORM_VERSION=$(curl -s https://api.github.com/repos/yannh/kubeconform/releases/latest | python3 -c "import sys,json; print(json.load(sys.stdin)['tag_name'])")
  curl -sSL "https://github.com/yannh/kubeconform/releases/download/${KUBECONFORM_VERSION}/kubeconform-linux-amd64.tar.gz" | tar -xz -C /tmp kubeconform
  /tmp/kubeconform -ignore-missing-schemas deploy/kubernetes/examples/*.yaml
  rm -f /tmp/kubeconform
else
  echo "Skipping yaml validations stage. kubeconform only supports x86"
fi
