*** Settings ***
Documentation       Extended tests related to auto-recovery functionality

Resource            ../../resources/microshift-host.resource
Resource            ../../resources/microshift-process.resource
Library             Collections
Library             DateTime
Library             ../../resources/libostree.py

Suite Setup         Setup
Suite Teardown      Teardown


*** Variables ***
${WORKDIR}      /var/lib/microshift-auto-recovery/


*** Test Cases ***
Using Systemd Dropin To React On Failure Of MicroShift
    [Documentation]    TODO

    Stop MicroShift
    Create Backup With Marker
    # Create drop-in with OnFailure=
    # Create service for the OnFailure=
    # daemon-reload
    
    # bootc usr-overlay
    # chmod -x /usr/bin/microshift
    # TIME=get time
    # restart microshift
    # check status of the microshift-auto-recovery, it should run after $TIME
    # check microshift data for the marker
    
    # Teardown: chmod +x /usr/bin/microshift; rm -f /var/lib/microshift/marker; start microshift


Auto Recovery On Red Boot
    [Documentation]    TODO

    Stop MicroShift
    Create Backup With Marker

    # create a red script that executes `microshift restore --auto-recovery`
    #   (only if it's not the reboot loop - don't interfere with greenboot)

    # restart greenboot
    # kill greenboot to trigger redboot?
    # check microshift data for the marker
    
    # Teardown: rm -f $red-script; start microshift


*** Keywords ***
Setup
    [Documentation]    Test suite setup
    Login MicroShift Host
    # Scenario needs to start with MicroShift running, so there's something to back up.
    # Stop MicroShift

Teardown
    [Documentation]    Test suite teardown
    Start MicroShift
    Logout MicroShift Host

Create Backup With Marker
    [Documentation]    Create a backup with a marker file inside.
    ...    Marker is later used to verify if the backup was actually restored.
    ${backup_path}=    Command Should Work    microshift backup --auto-recovery ${WORKDIR}
    Command Should Work    touch ${backup_path}/marker