#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "$0")"

: "${ANDROID_SDK_ROOT:?请设置 ANDROID_SDK_ROOT}"
: "${JAVA_HOME:?请设置 JAVA_HOME 指向 JDK 17 或 21}"
export PATH="$JAVA_HOME/bin:$PATH"
tools="${ANDROID_BUILD_TOOLS_DIR:-$ANDROID_SDK_ROOT/build-tools/35.0.0}"
platform="$ANDROID_SDK_ROOT/platforms/android-35/android.jar"

umask 077
mkdir -p build
temp_dir=$(mktemp -d "$PWD/build/work.XXXXXX")
trap 'rm -rf -- "$temp_dir"' EXIT
mkdir -p "$temp_dir/generated" "$temp_dir/classes" "$temp_dir/dex"

"$tools/aapt2" compile --dir res -o "$temp_dir/resources.zip"
"$tools/aapt2" link -I "$platform" --manifest AndroidManifest.xml --java "$temp_dir/generated" \
    -o "$temp_dir/unsigned.apk" "$temp_dir/resources.zip"
find src "$temp_dir/generated" -name '*.java' > "$temp_dir/sources.txt"
javac -encoding UTF-8 --release 11 -cp "$platform" -d "$temp_dir/classes" @"$temp_dir/sources.txt"
jar cf "$temp_dir/classes.jar" -C "$temp_dir/classes" .
"$tools/d8" --lib "$platform" --min-api 26 --output "$temp_dir/dex" "$temp_dir/classes.jar"
(cd -- "$temp_dir/dex" && zip -q -j "$temp_dir/unsigned.apk" classes.dex)
"$tools/zipalign" -f 4 "$temp_dir/unsigned.apk" "$temp_dir/aligned.apk"

keystore="${SUB2API_ANDROID_KEYSTORE:-$HOME/.android/debug.keystore}"
if [[ ! -f "$keystore" && -z "${SUB2API_ANDROID_KEYSTORE:-}" ]]; then
    mkdir -p "$(dirname -- "$keystore")"
    keytool -genkeypair -keystore "$keystore" -storepass android -keypass android -alias androiddebugkey \
        -dname 'CN=Android Debug,O=Android,C=US' -keyalg RSA -keysize 2048 -validity 10000
fi
if [[ -z "${SUB2API_ANDROID_KEYSTORE:-}" ]]; then
    "$tools/apksigner" sign --ks "$keystore" --ks-key-alias androiddebugkey --ks-pass pass:android \
        --out "$temp_dir/signed.apk" "$temp_dir/aligned.apk"
else
    "$tools/apksigner" sign --ks "$keystore" --out "$temp_dir/signed.apk" "$temp_dir/aligned.apk"
fi
"$tools/apksigner" verify "$temp_dir/signed.apk"
mv -f -- "$temp_dir/signed.apk" build/sub2api-alerts.apk
printf '%s\n' "$PWD/build/sub2api-alerts.apk"
