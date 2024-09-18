class Aicommit < Formula
    desc "AI-powered commit message generator"
    homepage "https://github.com/coder/aicommit"
    version "0.6.3"
    url "https://github.com/coder/aicommit/archive/refs/tags/v#{version}.tar.gz"
    sha256 "f42fac51fbe334f4d4057622b152eff168f4aa28d6da484af1cea966abd836a1"
    license "CC0-1.0"

    depends_on "go" => "1.21"
    depends_on "make" => :build

    def install
        ENV["VERSION"] = "v#{version}"
        system "make", "build"
        bin.install "bin/aicommit"
    end

    test do
        assert_match "aicommit v#{version}", shell_output("#{bin}/aicommit version")
    end
end

