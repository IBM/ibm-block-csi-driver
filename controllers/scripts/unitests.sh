#!/bin/bash
set -xe
coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir

# Add verbosity and process timeout to prevent hanging
# Use explicit test discovery to ensure tests are found
echo "Starting unit tests with coverage..."
echo "Coverage directory: $coveragedir"
echo "Python path: $PYTHONPATH"
echo "Working directory: $(pwd)"

# Run nosetests with increased verbosity and explicit test paths
nosetests --exe \
    --verbose \
    --with-coverage \
    --cover-xml \
    --cover-xml-file=$coveragedir/.coverage.xml \
    --cover-package=common \
    --cover-package=controllers \
    --with-xunit \
    --xunit-file=$coveragedir/.unitests.xml \
    --processes=4 \
    --process-timeout=600 \
    controllers/tests/ \
    $@

echo "Unit tests completed successfully"
