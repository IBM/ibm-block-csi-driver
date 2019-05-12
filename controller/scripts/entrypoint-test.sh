#!/bin/bash -x
coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir
exec /bin/nosetests --with-coverage --cover-xml --cover-xml-file=$coveragedir/.coverage.xml --with-xunit --xunit-file=$coveragedir/.unitests.xml $@
