#!/bin/bash
set -x
coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir
exec nosetests --exe --with-coverage --cover-xml --cover-xml-file=$coveragedir/.coverage.xml --cover-package=common --cover-package=controllers --with-xunit --xunit-file=$coveragedir/.unitests.xml $@
