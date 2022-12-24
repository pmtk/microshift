#!/usr/bin/env python

import os
import sys
import logging
import argparse
import subprocess
from collections import namedtuple

from git import Repo, PushInfo # GitPython
from github import GithubIntegration, Github, GithubException # pygithub
from pathlib import Path

APP_ID_ENV = "APP_ID"
KEY_ENV = "KEY"
ORG_ENV = "ORG"
REPO_ENV = "REPO"
AMD64_RELEASE_ENV = "AMD64_RELEASE"
ARM64_RELEASE_ENV = "ARM64_RELEASE"
JOB_NAME_ENV = "JOB_NAME"
BUILD_ID_ENV = "BUILD_ID"

BOT_REMOTE_NAME = "bot-creds"
REMOTE_ORIGIN = "origin"

REMOTE_DRY_RUN = False

logging.basicConfig(level=logging.INFO, format='%(asctime)s %(levelname)s %(message)s')

def try_get_env(var_name, die=True):
    val = os.getenv(var_name)
    if val is None or val == "":
        if die:
            logging.error(f"Could not get environment variable '{var_name}'")
            sys.exit(f"Could not get environment variable '{var_name}'")
        else:
            logging.info(f"Could not get environment variable '{var_name}' - ignoring")
            return ""
    return val


def run_rebase_sh(release_amd64, release_arm64):
   script_dir = os.path.abspath(os.path.dirname(__file__))
   args = [f"{script_dir}/rebase.sh", "to", release_amd64, release_arm64]
   logging.info(f"Running: '{' '.join(args)}'")
   output = []
   process = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
   for c in iter(lambda: process.stdout.read(1), b""):
       sys.stdout.buffer.write(c)
       output.append(c.decode('utf-8'))

   process.wait()
   sys.stdout.buffer.flush()
   print("------------------------------")
   logging.info(f"Script returned code: {process.returncode}")
   rr = namedtuple("RebaseResult", ["success", "output"])
   return rr(success=process.returncode == 0, output="".join(output))


def commit_str(commit):
    return f"{commit.hexsha[:8]} - {commit.summary}"


def create_or_get_pr_url(ghrepo):
    prs = ghrepo.get_pulls(base='main', head=f"{org}:{r.active_branch.name}", state="all")
    if prs.totalCount == 1:
        print(f"{prs[0].state.capitalize()} pull request exists already: {prs[0].html_url}")
    elif prs.totalCount > 1:
        print(f"Found several existing PRs for '{r.active_branch.name}': {[(x.state, x.html_url) for x in prs]}")
    else:
        body = f"{r.active_branch.name}\n\n/label tide/merge-method-squash"
        pr = ghrepo.create_pull(title=r.active_branch.name, body=body, base='main', head=r.active_branch.name, maintainer_can_modify=True)
        print(f"Created pull request: {pr.html_url}")


def get_installation_access_token(app_id, key_path, org, repo):
    integration = GithubIntegration(app_id, Path(key_path).read_text())
    app_installation = integration.get_installation(org, repo)
    if app_installation == None:
        sys.exit(f"Failed to get app_installation for {org}/{repo}. Response: {app_installation.raw_data}")
    return integration.get_access_token(app_installation.id).token


def make_sure_rebase_script_created_new_commits_or_exit(git_repo):
    if git_repo.active_branch.commit == git_repo.branches["main"].commit:
        logging.info(f"There's no new commit on branch {git_repo.active_branch} compared to 'main' "
                     "meaning that the rebase.sh script didn't create any commits and "
                     "MicroShift is already rebased on top of given release.\n"
                     f"Last commit: {git_repo.active_branch.commit.hexsha[:8]} - \n\n{git_repo.active_branch.commit.summary}'")
        sys.exit(0)


def get_remote_with_token(git_repo, token, org, repo):
    remote_url = f"https://x-access-token:{token}@github.com/{org}/{repo}"
    try:
        remote = git_repo.remote(BOT_REMOTE_NAME)
        remote.set_url(remote_url)
    except ValueError:
        git_repo.create_remote(BOT_REMOTE_NAME, remote_url)

    remote = git_repo.remote(BOT_REMOTE_NAME)
    return remote


def try_get_rebase_branch_from_remote(remote, branch_name):
    remote.fetch()
    matching_remote_branches = [ ref for ref in remote.refs if BOT_REMOTE_NAME + "/" + branch_name == ref.name ]

    if len(matching_remote_branches) == 0:
        logging.info(f"Branch '{branch_name}' does not exist on remote")
        return None

    if len(matching_remote_branches) > 1:
        logging.error(f"Found more than one branch matching '{branch_name}' on remote: {matching_remote_branches}. Taking first one")
        return matching_remote_branches[0]

    if len(matching_remote_branches) == 1:
        logging.info(f"Branch '{branch_name}' already exists on remote")
        return matching_remote_branches[0]


def is_local_branch_based_on_newer_main_commit(git_repo, remote_branch_name, local_branch_name):
    """
    Compares local and remote rebase branches by looking at their start on main branch.
    Returns True if local branch is starts on newer commit and needs to be pushed to remote, otherwise False.
    """
    remote_merge_base = git_repo.merge_base("main", remote_branch_name)
    local_merge_base = git_repo.merge_base("main", local_branch_name)

    if remote_merge_base[0] == local_merge_base[0]:
        logging.info(f"Remote branch is up to date. Branch-off commit: {commit_str(remote_merge_base[0])}")
        return False
    else:
        logging.info(f"Remote branch is older - it needs updating. "
                f"Remote branch is on top of main's commit: '{commit_str(remote_merge_base[0])}'. "
                f"Local branch is on top of main's commit '{commit_str(local_merge_base[0])}'")
        return True


def try_get_pr(gh_repo, org, branch_name):
    prs = gh_repo.get_pulls(base='main', head=f"{org}:{branch_name}", state="all")

    if prs.totalCount == 0:
        logging.info(f"PR for branch {branch_name} does not exist yet on {gh_repo.full_name}")
        return None

    if prs.totalCount > 1:
        logging.warning(f"Found more than one PR for branch {branch_name} on {gh_repo.full_name} - this is unexpected, continuing with first one of: {[(x.state, x.html_url) for x in prs]}")
        return prs[0]

    if prs.totalCount == 1:
        logging.info(f"Found {prs[0].state} PR for branch {branch_name} on {gh_repo.full_name}: {prs[0].html_url}")
        return prs[0]


def generate_pr_description(branch_name, amd_tag, arm_tag, prow_job_url, rebase_script_succeded):
    base = (f"amd64: {amd_tag}\n"
            f"arm64: {arm_tag}\n"
            f"prow job: {prow_job_url}\n"
            "\n"
            "/label tide/merge-method-squash")
    return ("# rebase.sh failed - check committed rebase_sh.log\n" + base
            if not rebase_script_succeded
            else base)


def create_pr(gh_repo, branch_name, title, desc):
    if REMOTE_DRY_RUN:
        logging.info(f"[DRY RUN] Create PR: branch='{branch_name}', title='{title}', desc='{desc}'")
        return

    pr = gh_repo.create_pull(title=title, body=desc, base='main', head=branch_name, maintainer_can_modify=True)
    print(f"Created pull request: {pr.html_url}")
    return pr


def update_pr(pr, title, desc):
    if REMOTE_DRY_RUN:
        logging.info(f"[DRY RUN] Update PR: title='{pr_title}', desc='{desc}'")
        return

    pr.update(title=pr_title, body=desc) # arm64 release or prow job url might've changed


def push_branch_or_die(remote, branch_name):
    if REMOTE_DRY_RUN:
        logging.info(f"[DRY RUN] git push --force {branch_name}")
        return

    # TODO add retries
    push_result = remote.push(branch_name, force=True)

    if len(push_result) != 1:
        sys.exit(f"Unexpected amount ({len(push_result)}) of items in push_result: {push_result}")
    if push_result[0].flags & PushInfo.ERROR:
        sys.exit(f"Pushing branch failed: {push_result[0].summary}")
    if push_result[0].flags & PushInfo.FORCED_UPDATE:
        logging.info(f"Branch '{branch_name}' existed and was updated (force push)")


def get_release_tag(release):
    parts = release.split(":")
    if len(parts) == 2:
        return parts[1]
    else:
        logging.error(f"Couldn't find tag in '{release}' - using it as is as branch name")
        return release


def try_create_prow_job_url():
    job_name = try_get_env(JOB_NAME_ENV, False)
    build_id = try_get_env(BUILD_ID_ENV, False)
    if job_name != "" and build_id != "":
        url = f"https://prow.ci.openshift.org/view/gs/origin-ci-test/logs/{JOB_NAME}/{BUILD_ID}"
        logging.info(f"Inferred probable prow job url: {url}")
        return url
    else:
        logging.warning(f"Couldn't infer prow job url. Env vars: '{JOB_NAME_ENV}'='{job_name}', '{BUILD_ID_ENV}'='{build_id}'")
        return ""


def create_pr_title(branch_name, successful_rebase):
    return branch_name if successful_rebase else f"**FAILURE** {branch_name}"


def get_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--dry-run", help="Enables remote dry run - there'll be no changes to remote repository", action="store_true")
    return parser.parse_args()


def main():
    app_id = try_get_env(APP_ID_ENV)
    key_path = try_get_env(KEY_ENV)
    org = try_get_env(ORG_ENV)
    repo = try_get_env(REPO_ENV)
    release_amd = try_get_env(AMD64_RELEASE_ENV)
    release_arm = try_get_env(ARM64_RELEASE_ENV)

    REMOTE_DRY_RUN = get_args().dry_run

    token = get_installation_access_token(app_id, key_path, org, repo)
    gh_repo = Github(token).get_repo(f"{org}/{repo}")
    git_repo = Repo('.')

    rebase_result = run_rebase_sh(release_amd, release_arm)
    if rebase_result.success:
        # TODO Consider posting a comment that job ran, but there's nothing new instead of exiting.
        # This is low prio because it's more likely that same rebase will be on top of newer main head.
        # Also needs changing github app's permissions (PR is issue under the hood and it's "issue comment",
        # PR comments are specific to PR's diff) which need to be accepted by gh org's admins
        make_sure_rebase_script_created_new_commits_or_exit(git_repo)
    else:
        logging.warning("Rebase script failed - everything will be committed")
        with open('rebase_sh.log', 'w') as writer:
            writer.write(rebase_result.output)
        if git_repo.active_branch.name == "main":
            # rebase.sh didn't get to creating a branch
            branch = git_repo.create_head(get_release_tag(release_amd))
            branch.checkout()
        git_repo.git.add(A=True)
        git_repo.index.commit("rebase.sh failure artifacts")

    rebase_branch_name = git_repo.active_branch.name
    git_remote = get_remote_with_token(git_repo, token, org, repo)
    remote_branch = try_get_rebase_branch_from_remote(git_remote, rebase_branch_name) # {BOT_REMOTE_NAME}/{rebase_branch_name}

    remote_branch_does_not_exists = remote_branch == None
    remote_branch_exists_and_needs_update = remote_branch != None and is_local_branch_based_on_newer_main_commit(git_repo, remote_branch.name, rebase_branch_name)
    if remote_branch_does_not_exists or remote_branch_exists_and_needs_update:
        push_branch_or_die(git_remote, rebase_branch_name)

    prow_job_url = try_create_prow_job_url()
    pr_title = create_pr_title(rebase_branch_name, rebase_result.success)
    desc = generate_pr_description(rebase_branch_name, get_release_tag(release_amd), get_release_tag(release_arm), prow_job_url, rebase_result.success)

    pr = try_get_pr(gh_repo, org, rebase_branch_name)
    if pr == None:
        create_pr(gh_repo, rebase_branch_name, pr_title, desc)
    else:
        update_pr(pr, pr_title, desc)

    os.exit(0 if rebase_result.success else 1)


if __name__ == "__main__":
    main()
