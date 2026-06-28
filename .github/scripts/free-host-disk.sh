#!/usr/bin/env bash
# Free host disk we don't use (unused language toolchains) on the CI runner.
# Each removal is best-effort (|| true) because the toolchains present vary by
# runner image.

echo "================ REMOVE JAVA (JDKs) ================"
echo "Removing all JDKs from /usr/lib/jvm"
sudo rm -rf /usr/lib/jvm || true
df -h
echo

echo "================ REMOVE .NET SDKs ================"
echo "Removing .NET SDKs from /usr/share/dotnet"
sudo rm -rf /usr/share/dotnet || true
df -h
echo

echo "================ REMOVE SWIFT TOOLCHAIN ================"
echo "Removing Swift from /usr/share/swift"
sudo rm -rf /usr/share/swift || true
df -h
echo

echo "================ REMOVE HASKELL (GHCUP) ================"
echo "Removing GHC toolchains from /usr/local/.ghcup"
sudo rm -rf /usr/local/.ghcup || true
df -h
echo

echo "================ REMOVE JULIA ================"
echo "Removing Julia installations from /usr/local/julia*"
sudo rm -rf /usr/local/julia* || true
df -h
echo

echo "================ REMOVE ANDROID SDKs ================"
echo "Removing Android SDKs from /usr/local/lib/android"
sudo rm -rf /usr/local/lib/android || true
df -h
echo

echo "================ REMOVE CHROMIUM ================"
echo "Removing Chromium from /usr/local/share/chromium"
sudo rm -rf /usr/local/share/chromium || true
df -h
echo

echo "================ REMOVE EDGE & CHROME BUILDS ================"
echo "Removing Microsoft Edge and Google Chrome from /opt"
sudo rm -rf /opt/microsoft /opt/google || true
df -h
echo

echo "================ REMOVE POWERSHELL ================"
echo "Removing PowerShell from /usr/local/share/powershell"
sudo rm -rf /usr/local/share/powershell || true
df -h
echo

# Optional – huge space saver on GitHub runners
# echo "================ REMOVE HOSTED TOOLCACHE ================"
# echo "Removing GitHub hosted toolcache"
# sudo rm -rf /opt/hostedtoolcache || true
# df -h
# echo

du -d1 -h /opt/hostedtoolcache | sort -h -k1
