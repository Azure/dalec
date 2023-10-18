# dalec-reirectio

This binary is used in the test phase of the dalec build process to redirect
stdio streams to a file that can be read by the test harness.

This is included in the frontend image and mounted into the test harness only when
a test case requests to have 1 or more stdio streams captured.