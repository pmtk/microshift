import subprocess
import time
from fastmcp import FastMCP, Context
from typing import Annotated, Literal
from pydantic import Field

mcp = FastMCP(name="Demo")


# @mcp.tool()
# def get_technologies() -> list[str]:
#     """Get technologies used in the test harness"""
#     return ["ostree", "bootc"]

@mcp.tool()
def get_scenarios(
    # technology: Annotated[str, Literal["bootc", "ostree"], Field(description="Technology of scenarios")],
    type: Annotated[str, Literal["periodics", "presubmits"], Field(description="Type of scenarios")],
) -> list[str]:
    """Get a list of scenarios for a given type"""
    if type != "presubmits" and type != "periodics":
        raise ValueError("Type must be either presubmits or periodics")

    cmd = f"ls -1 ../scenarios-bootc/{type}/"
    output = subprocess.check_output(cmd, shell=True)
    return [f"scenarios-bootc/{type}/{line.strip()}" for line in output.decode("utf-8").splitlines()]

@mcp.tool()
def get_images_used_in_scenario(
    scenario: str,
) -> list[str]:
    """Get a list of images used in a given scenario"""
    print(f"Getting images used in scenario: {scenario}")
    try:
        # Read the scenario file
        with open(f"../{scenario}", 'r') as f:
            content = f.read()
        
        print(f"Content: {content}")

        # Look for image patterns in the content
        images = []
        
        # Pattern 1: Look for start_image= assignments
        import re
        start_image_pattern = r'start_image=([^\s]+)'
        start_matches = re.findall(start_image_pattern, content)
        print(f"Start matches: {start_matches}")
        images.extend(start_matches)
        
        # Pattern 2: Look for prepare_kickstart calls with image parameters
        # Find lines containing 'prepare_kickstart' and extract words containing -bootc-
        kickstart_lines = [line for line in content.splitlines() if 'prepare_kickstart' in line]
        print(f"Kickstart lines: {kickstart_lines}")
        for line in kickstart_lines:
            # Split the line and look for words containing -bootc-
            words = line.split()
            bootc_words = [word for word in words if '-bootc-' in word and not '.ks' in word]
            print(f"Bootc words found in line '{line}': {bootc_words}")
            images.extend(bootc_words)
        
        # Pattern 3: Look for TARGET_REF assignments
        target_ref_pattern = r'TARGET_REF:([^\s]+)'
        target_ref_matches = re.findall(target_ref_pattern, content)
        print(f"Target ref matches: {target_ref_matches}")
        images.extend(target_ref_matches)
        
        # Remove duplicates and return
        return list(set(images))
        
    except FileNotFoundError:
        print(f"Scenario file not found: {scenario}")
        return []
    except Exception as e:
        print(f"Error reading scenario file: {e}")
        return []

@mcp.tool()
async def build_image(type: str, image: str, ctx: Context) -> None:
    """Build a bootc image for test harness"""
    await ctx.info(f"Building {type} image: {image}")
    await ctx.report_progress(progress=0, total=100)
    time.sleep(1)
    await ctx.report_progress(progress=20, total=100)
    time.sleep(1)
    await ctx.report_progress(progress=60, total=100)
    time.sleep(1)
    await ctx.report_progress(progress=100, total=100)
    await ctx.info(f"Built {type} image: {image}")


@mcp.tool()
def get_images(technology: Annotated[str, Literal["bootc", "ostree"], Field(description="Technology of images")]) -> list[str]:
    """Get a list of images involved in the test harness"""
    return [
        "rhel94-test-agent",
        "rhel96-test-agent",
        "rhel94-bootc-prel",
        "rhel94-bootc-yminus2",
        "rhel96-bootc-brew",
        "rhel96-bootc-crel-optionals",
        "rhel96-bootc-crel",
        "microshift-copy-images.conf.template",
        "microshift-copy-images.template",
        "rhel96-bootc-crel-isolated",
        "cos9-bootc-source",
        "microshift-ovsdb-ownership.conf.template",
        "rhel96-bootc-source-base",
        "rhel96-bootc-source",
        "rhel96-bootc-source-aux",
        "rhel96-bootc-source-fake-next-minor",
        "rhel96-bootc-source-fips",
        "rhel96-bootc-source-optionals",
        "rhel96-bootc-source-ai-model-serving",
        "rhel96-bootc-source-isolated",
        "cos9-bootc-source-fips",
        "cos9-bootc-source-flannel",
        "cos9-bootc-source-isolated",
        "cos9-bootc-source-optionals",
    ]

# Add a dynamic greeting resource
@mcp.resource("greeting://{name}")
def get_greeting(name: str) -> str:
    """Get a personalized greeting"""
    return f"Hello, {name}!"

# @mcp.resource("microshift-tests://resourcetypes")
# def get_test_resources() -> list:
#     """Get a MicroShift test resources"""
#     return ["images", "scenarios"]

# @mcp.resource("microshift-tests://resource/{resource}")
# def get_microshift_test_resource(resource: str) -> str:
#     """Get a MicroShift test resource"""
#     return f"Hello, {resource}!"



if __name__ == "__main__":
    #mcp.run(transport="sse", host="0.0.0.0", port=3010, log_level="debug")
    mcp.run(transport="streamable-http", host="0.0.0.0", port=3010, log_level="debug")