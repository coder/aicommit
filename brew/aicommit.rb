class Aicommit < Formula
    desc "AI-powered commit message generator"
    homepage "https://github.com/coder/aicommit"
    version "0.6.2"
    url "https://github.com/coder/aicommit/archive/refs/tags/v#{version}.tar.gz"
    sha256 "04a980875e97aebc0f7bbeaa183aa5542b18c6e009505babbd494fe0fe62b18e"
    license "CC0-1.0"

    depends_on "go" => "1.21"
    depends_on "make" => :build

    def install
        version = "v#{version}"
        ENV["VERSION"] = version
        system "make", "build", "VERSION=#{version}"
        bin.install "bin/aicommit"
    end

    test do
        assert_match(/aicommit \S+/, shell_output("#{bin}/aicommit version"))
    end
end

