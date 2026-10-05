# Mirrors the formula GoReleaser publishes to lleontor705/homebrew-tap
# (`.goreleaser.yaml`, `brews:` -> `Formula/cortex.rb`). Stable URLs and
# checksums are stamped per release; this in-repo copy exists for review only
# and must never be installed directly.
class Cortex < Formula
  desc "Persistent memory for AI coding agents — knowledge graph, importance scoring, vector search"
  homepage "https://github.com/lleontor705/cortex"
  license "MIT"
  head "https://github.com/lleontor705/cortex.git", branch: "main"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/lleontor705/cortex/releases/download/vVERSION/cortex_VERSION_darwin_arm64.tar.gz"
      sha256 "SHA256_DARWIN_ARM64"
    else
      url "https://github.com/lleontor705/cortex/releases/download/vVERSION/cortex_VERSION_darwin_amd64.tar.gz"
      sha256 "SHA256_DARWIN_AMD64"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/lleontor705/cortex/releases/download/vVERSION/cortex_VERSION_linux_arm64.tar.gz"
      sha256 "SHA256_LINUX_ARM64"
    else
      url "https://github.com/lleontor705/cortex/releases/download/vVERSION/cortex_VERSION_linux_amd64.tar.gz"
      sha256 "SHA256_LINUX_AMD64"
    end
  end

  livecheck do
    strategy :github_latest
  end

  depends_on "go" => :build

  def install
    if build.head?
      system "make", "build"
      bin.install "bin/cortex"
    else
      bin.install "cortex"
    end
  end

  test do
    assert_match "cortex", shell_output("#{bin}/cortex --version")
  end
end
