#!/bin/bash -x
exec /bin/nosetests --with-coverage --cover-xml --cover-xml-file=/driver/coverage/.coverage.xml --with-xunit --xunit-file=/driver/coverage/.unitests.xml $@
