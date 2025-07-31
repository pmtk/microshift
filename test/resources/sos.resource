*** Settings ***
Documentation       Keywords for collecting minimal SOS Report after failed test case

Resource            microshift-host.resource


*** Keywords ***
Run Sos Report
    [Documentation]    Creates a minimal SOS report. By default only 'microshift' profiles is enabled.
    ...    It's recommended to supply comma-separated list of extra namespaces for inclusion in the report.
    [Arguments]    ${profiles}=microshift    ${plugins}=${EMPTY}    ${namespaces}=${EMPTY}

    IF    "${namespaces}" != "${EMPTY}"
        ${namespaces_arg}=    Set Variable    -k microshift.add-namespaces=${namespaces}
    END

    Command Execution    microshift-sos-report --profiles "${profiles}" --plugins "${plugins}" ${namespaces_arg}
