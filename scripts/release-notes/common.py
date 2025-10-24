import pathlib
import datetime

MAX_RELEASE_NOTE_BODY_SIZE = 125000
TRUNCATED_MESSAGE = '\n\n(release notes were truncated)\n\n'


def get_version_from_makefile():
    # The script runs as
    # .../microshift/scripts/release-notes/common.py
    # and we want the path to
    # .../microshift
    root_dir = pathlib.Path(__file__).parent.parent.parent
    version_makefile = root_dir / 'Makefile.version.aarch64.var'
    # Makefile contains something like
    #   OCP_VERSION := 4.16.0-0.nightly-arm64-2024-03-13-041907
    # and we want this ^^^^
    #
    # We get it as ['4', '16'] to make the next part of the process of
    # building the list of versions to scan easier.
    _full_version = version_makefile.read_text('utf8').split('=')[-1].strip()
    major, minor = _full_version.split('.')[:2]
    return major, minor


def publish_release(ghutils, gitutils, new_release, preamble, prerelease=False):
    """Does the work to tag and publish a release.
    """
    release_name = new_release.release_name
    commit_sha = new_release.commit_sha
    release_date = new_release.release_date

    if not gitutils.tag_exists(release_name):
        # release_date looks like 202402022103
        buildtime = datetime.datetime.strptime(release_date, '%Y%m%d%H%M')
        gitutils.create_tag(release_name, commit_sha, buildtime)

    # Get the previous tag on the branch as the starting point for the
    # release notes.
    previous_tag = gitutils.get_previous_tag(release_name)

    # Auto-generate the release notes ourselves, add the preamble,
    # then make sure the results fit within the size limits imposed by
    # the API.
    generated_notes = ghutils.generate_release_notes(previous_tag, release_name, commit_sha)
    notes = f'{preamble}\n{generated_notes["body"]}'
    if len(notes) > MAX_RELEASE_NOTE_BODY_SIZE:
        lines = notes.splitlines()
        last_line = lines[-1]
        notes_content_we_can_truncate = notes[:-len(last_line)]
        amount_we_can_keep = MAX_RELEASE_NOTE_BODY_SIZE - len(last_line) - len(TRUNCATED_MESSAGE)
        truncated = notes_content_we_can_truncate[:amount_we_can_keep]
        if truncated[-1] == '\n':
            notes_to_keep = truncated
        else:
            # don't leave a partial line
            notes_to_keep = truncated.rpartition('\n')[0].rstrip()
        notes = f'{notes_to_keep}{TRUNCATED_MESSAGE}{last_line}'

    gitutils.push(release_name)

    # Create draft release with message that includes download URLs and history
    ghutils.create_release(release_name, notes, prerelease=prerelease)
