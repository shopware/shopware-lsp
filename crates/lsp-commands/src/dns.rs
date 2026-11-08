//! A small DNS resolver for the GitHub fallback of `getBlockDiff`.
//!
//! Linux release binaries link glibc statically. A static glibc still loads
//! NSS modules for `getaddrinfo` at run time, and when the host's glibc
//! differs from the build's the process crashes. This resolver avoids NSS:
//! it answers from `/etc/hosts`, then queries the nameservers listed in
//! `/etc/resolv.conf` over UDP (A and AAAA, recursion desired).

use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr, UdpSocket};
use std::time::Duration;

use simple_dns::rdata::RData;
use simple_dns::{CLASS, Name, Packet, PacketFlag, QCLASS, QTYPE, Question, TYPE};

const QUERY_TIMEOUT: Duration = Duration::from_secs(3);

/// `ureq` resolver backed by [`resolve`].
pub(crate) struct Resolver;

impl ureq::Resolver for Resolver {
    fn resolve(&self, netloc: &str) -> std::io::Result<Vec<SocketAddr>> {
        // Never call `to_socket_addrs` here: for host names it goes through
        // glibc's `getaddrinfo`, which is exactly what this resolver avoids.
        if let Ok(address) = netloc.parse::<SocketAddr>() {
            return Ok(vec![address]);
        }
        let (host, port) = netloc
            .rsplit_once(':')
            .and_then(|(host, port)| Some((host, port.parse::<u16>().ok()?)))
            .ok_or_else(|| std::io::Error::other(format!("invalid address {netloc}")))?;
        let addresses = resolve(host)?;
        Ok(addresses
            .into_iter()
            .map(|address| SocketAddr::new(address, port))
            .collect())
    }
}

/// Addresses of `host` from `/etc/hosts`, else from the configured
/// nameservers.
pub(crate) fn resolve(host: &str) -> std::io::Result<Vec<IpAddr>> {
    if let Ok(hosts) = std::fs::read_to_string("/etc/hosts") {
        let found = hosts_lookup(&hosts, host);
        if !found.is_empty() {
            return Ok(found);
        }
    }
    let config = std::fs::read_to_string("/etc/resolv.conf").unwrap_or_default();
    let mut servers = nameservers(&config);
    if servers.is_empty() {
        servers.push(IpAddr::V4(Ipv4Addr::LOCALHOST));
    }
    let mut last_error = None;
    for server in servers {
        let mut addresses = Vec::new();
        for kind in [TYPE::A, TYPE::AAAA] {
            match query(server, host, kind) {
                Ok(found) => addresses.extend(found),
                Err(error) => last_error = Some(error),
            }
        }
        if !addresses.is_empty() {
            return Ok(addresses);
        }
    }
    Err(last_error.unwrap_or_else(|| std::io::Error::other(format!("no address found for {host}"))))
}

fn query(server: IpAddr, host: &str, kind: TYPE) -> std::io::Result<Vec<IpAddr>> {
    let bind: SocketAddr = match server {
        IpAddr::V4(_) => (Ipv4Addr::UNSPECIFIED, 0).into(),
        IpAddr::V6(_) => (Ipv6Addr::UNSPECIFIED, 0).into(),
    };
    let socket = UdpSocket::bind(bind)?;
    socket.set_read_timeout(Some(QUERY_TIMEOUT))?;
    socket.connect(SocketAddr::new(server, 53))?;
    let id = query_id(host, kind);
    let name = Name::new(host).map_err(std::io::Error::other)?;
    let mut packet = Packet::new_query(id);
    packet.set_flags(PacketFlag::RECURSION_DESIRED);
    packet.questions.push(Question::new(
        name,
        QTYPE::TYPE(kind),
        QCLASS::CLASS(CLASS::IN),
        false,
    ));
    socket.send(&packet.build_bytes_vec().map_err(std::io::Error::other)?)?;
    let mut buffer = [0u8; 4096];
    loop {
        let length = socket.recv(&mut buffer)?;
        let Ok(reply) = Packet::parse(&buffer[..length]) else {
            continue;
        };
        if reply.id() != id {
            continue;
        }
        return Ok(reply
            .answers
            .iter()
            .filter_map(|record| match &record.rdata {
                RData::A(a) => Some(IpAddr::V4(Ipv4Addr::from(a.address))),
                RData::AAAA(aaaa) => Some(IpAddr::V6(Ipv6Addr::from(aaaa.address))),
                _ => None,
            })
            .collect());
    }
}

/// A query id that differs per request without a random number generator.
fn query_id(host: &str, kind: TYPE) -> u16 {
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |duration| duration.subsec_nanos());
    let mut hash = nanos ^ u32::from(u16::from(kind)) ^ std::process::id();
    for byte in host.bytes() {
        hash = hash.rotate_left(5) ^ u32::from(byte);
    }
    (hash ^ (hash >> 16)) as u16
}

/// Addresses for `host` in an `/etc/hosts` file.
fn hosts_lookup(hosts: &str, host: &str) -> Vec<IpAddr> {
    let mut result = Vec::new();
    for line in hosts.lines() {
        let line = line.split('#').next().unwrap_or_default();
        let mut fields = line.split_whitespace();
        let Some(address) = fields.next().and_then(|field| field.parse::<IpAddr>().ok()) else {
            continue;
        };
        if fields.any(|name| name.eq_ignore_ascii_case(host)) {
            result.push(address);
        }
    }
    result
}

/// `nameserver` entries of a `resolv.conf`.
fn nameservers(config: &str) -> Vec<IpAddr> {
    config
        .lines()
        .filter_map(|line| {
            let mut fields = line.split_whitespace();
            (fields.next() == Some("nameserver"))
                .then(|| fields.next()?.split('%').next()?.parse().ok())
                .flatten()
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn literal_addresses_skip_dns() {
        use ureq::Resolver as _;
        assert_eq!(
            Resolver.resolve("127.0.0.1:443").unwrap(),
            ["127.0.0.1:443".parse::<SocketAddr>().unwrap()]
        );
        assert_eq!(
            Resolver.resolve("[::1]:80").unwrap(),
            ["[::1]:80".parse::<SocketAddr>().unwrap()]
        );
        assert!(Resolver.resolve("no-port").is_err());
    }

    #[test]
    fn parses_hosts_and_resolv_conf() {
        let hosts = "127.0.0.1 localhost\n# comment\n10.0.0.5  github.internal  Raw.Example # alias\n::1 localhost ip6-localhost\n";
        assert_eq!(
            hosts_lookup(hosts, "raw.example"),
            [IpAddr::V4(Ipv4Addr::new(10, 0, 0, 5))]
        );
        assert_eq!(hosts_lookup(hosts, "localhost").len(), 2);
        assert!(hosts_lookup(hosts, "missing").is_empty());
        let config = "# generated\nsearch lan\nnameserver 127.0.0.53\nnameserver fe80::1%eth0\nnameserver bogus\noptions edns0\n";
        assert_eq!(
            nameservers(config),
            [
                IpAddr::V4(Ipv4Addr::new(127, 0, 0, 53)),
                "fe80::1".parse::<IpAddr>().unwrap()
            ]
        );
    }

    /// Needs network access: `cargo test -p swlsp-lsp-commands -- --ignored`.
    #[test]
    #[ignore = "queries the configured nameservers"]
    fn resolves_github_raw_host() {
        assert!(!resolve("raw.githubusercontent.com").unwrap().is_empty());
    }
}
