//! The MessagePack subset used by the stub catalog: nil, booleans,
//! integers, strings, binary blobs, arrays, and maps. The reader borrows
//! strings and blobs from its input.

use swlsp_php_types::PersistedError;

/// A malformed stub catalog.
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
pub enum Error {
    #[error("unexpected end of data")]
    UnexpectedEnd,
    #[error("expected {expected}, found 0x{code:02x}")]
    UnexpectedCode { expected: &'static str, code: u8 },
    #[error("invalid msgpack code 0xc1")]
    ReservedCode,
    #[error("integer {0} out of range")]
    IntegerOutOfRange(i64),
    #[error(transparent)]
    InvalidUtf8(#[from] std::str::Utf8Error),
    #[error("invalid {field} {value}")]
    InvalidEnum { field: &'static str, value: u8 },
    #[error("parse persisted PHP type {text:?}: {source}")]
    Type {
        text: String,
        #[source]
        source: PersistedError,
    },
    #[error("decode PHP stub catalog: unsupported format {0}")]
    UnsupportedFormat(u8),
    #[error("decode PHP stub catalog: invalid version count {0}")]
    VersionCount(usize),
}

pub type Result<T> = std::result::Result<T, Error>;

fn unexpected<T>(expected: &'static str, code: u8) -> Result<T> {
    Err(Error::UnexpectedCode { expected, code })
}

pub struct Reader<'a> {
    data: &'a [u8],
    position: usize,
}

impl<'a> Reader<'a> {
    pub fn new(data: &'a [u8]) -> Self {
        Reader { data, position: 0 }
    }

    fn take(&mut self, count: usize) -> Result<&'a [u8]> {
        let end = self
            .position
            .checked_add(count)
            .filter(|end| *end <= self.data.len())
            .ok_or(Error::UnexpectedEnd)?;
        let bytes = &self.data[self.position..end];
        self.position = end;
        Ok(bytes)
    }

    fn byte(&mut self) -> Result<u8> {
        Ok(self.take(1)?[0])
    }

    fn peek(&self) -> Result<u8> {
        self.data
            .get(self.position)
            .copied()
            .ok_or(Error::UnexpectedEnd)
    }

    fn be(&mut self, count: usize) -> Result<u64> {
        Ok(self
            .take(count)?
            .iter()
            .fold(0u64, |value, byte| (value << 8) | u64::from(*byte)))
    }

    /// Consumes a nil marker when present.
    pub fn nil(&mut self) -> Result<bool> {
        if self.peek()? == 0xc0 {
            self.position += 1;
            return Ok(true);
        }
        Ok(false)
    }

    pub fn bool(&mut self) -> Result<bool> {
        match self.byte()? {
            0xc2 | 0xc0 => Ok(false),
            0xc3 => Ok(true),
            code => unexpected("bool", code),
        }
    }

    pub fn int(&mut self) -> Result<i64> {
        let code = self.byte()?;
        Ok(match code {
            0x00..=0x7f => i64::from(code),
            0xe0..=0xff => i64::from(code as i8),
            0xc0 => 0,
            0xcc => self.be(1)? as i64,
            0xcd => self.be(2)? as i64,
            0xce => self.be(4)? as i64,
            0xcf => self.be(8)? as i64,
            0xd0 => i64::from(self.be(1)? as u8 as i8),
            0xd1 => i64::from(self.be(2)? as u16 as i16),
            0xd2 => i64::from(self.be(4)? as u32 as i32),
            0xd3 => self.be(8)? as i64,
            _ => return unexpected("integer", code),
        })
    }

    pub fn uint<T: TryFrom<i64>>(&mut self) -> Result<T> {
        let value = self.int()?;
        T::try_from(value).map_err(|_| Error::IntegerOutOfRange(value))
    }

    pub fn str(&mut self) -> Result<&'a str> {
        let code = self.byte()?;
        let length = match code {
            0xa0..=0xbf => usize::from(code & 0x1f),
            0xd9 => self.be(1)? as usize,
            0xda => self.be(2)? as usize,
            0xdb => self.be(4)? as usize,
            0xc4 => self.be(1)? as usize,
            0xc5 => self.be(2)? as usize,
            0xc6 => self.be(4)? as usize,
            0xc0 => 0,
            _ => return unexpected("string", code),
        };
        Ok(std::str::from_utf8(self.take(length)?)?)
    }

    pub fn bin(&mut self) -> Result<&'a [u8]> {
        let code = self.byte()?;
        let length = match code {
            0xc4 | 0xd9 => self.be(1)? as usize,
            0xc5 | 0xda => self.be(2)? as usize,
            0xc6 | 0xdb => self.be(4)? as usize,
            0xa0..=0xbf => usize::from(code & 0x1f),
            0xc0 => 0,
            _ => return unexpected("binary", code),
        };
        self.take(length)
    }

    /// An array header; nil counts as an empty array.
    pub fn array(&mut self) -> Result<usize> {
        let code = self.byte()?;
        Ok(match code {
            0x90..=0x9f => usize::from(code & 0x0f),
            0xdc => self.be(2)? as usize,
            0xdd => self.be(4)? as usize,
            0xc0 => 0,
            _ => return unexpected("array", code),
        })
    }

    /// A map header; nil counts as an empty map.
    pub fn map(&mut self) -> Result<usize> {
        let code = self.byte()?;
        Ok(match code {
            0x80..=0x8f => usize::from(code & 0x0f),
            0xde => self.be(2)? as usize,
            0xdf => self.be(4)? as usize,
            0xc0 => 0,
            _ => return unexpected("map", code),
        })
    }

    /// Skips one complete value.
    pub fn skip(&mut self) -> Result<()> {
        let code = self.peek()?;
        match code {
            0x00..=0x7f | 0xe0..=0xff | 0xc0 | 0xc2 | 0xc3 => {
                self.position += 1;
            }
            0xcc..=0xd3 => {
                self.int()?;
            }
            0xca => {
                self.take(5)?;
            }
            0xcb => {
                self.take(9)?;
            }
            0xa0..=0xbf | 0xd9..=0xdb => {
                self.str()?;
            }
            0xc4..=0xc6 => {
                self.bin()?;
            }
            0x90..=0x9f | 0xdc | 0xdd => {
                for _ in 0..self.array()? {
                    self.skip()?;
                }
            }
            0x80..=0x8f | 0xde | 0xdf => {
                for _ in 0..self.map()? * 2 {
                    self.skip()?;
                }
            }
            0xd4 => {
                self.take(3)?;
            }
            0xd5 => {
                self.take(4)?;
            }
            0xd6 => {
                self.take(6)?;
            }
            0xd7 => {
                self.take(10)?;
            }
            0xd8 => {
                self.take(18)?;
            }
            0xc7 => {
                self.position += 1;
                let length = self.be(1)? as usize;
                self.take(length + 1)?;
            }
            0xc8 => {
                self.position += 1;
                let length = self.be(2)? as usize;
                self.take(length + 1)?;
            }
            0xc9 => {
                self.position += 1;
                let length = self.be(4)? as usize;
                self.take(length + 1)?;
            }
            0xc1 => return Err(Error::ReservedCode),
        }
        Ok(())
    }
}

#[derive(Default)]
pub struct Writer {
    pub data: Vec<u8>,
}

impl Writer {
    pub fn nil(&mut self) {
        self.data.push(0xc0);
    }

    pub fn bool(&mut self, value: bool) {
        self.data.push(if value { 0xc3 } else { 0xc2 });
    }

    pub fn uint(&mut self, value: u64) {
        if value < 0x80 {
            self.data.push(value as u8);
        } else if value <= 0xff {
            self.data.extend_from_slice(&[0xcc, value as u8]);
        } else if value <= 0xffff {
            self.data.push(0xcd);
            self.data.extend_from_slice(&(value as u16).to_be_bytes());
        } else if value <= 0xffff_ffff {
            self.data.push(0xce);
            self.data.extend_from_slice(&(value as u32).to_be_bytes());
        } else {
            self.data.push(0xcf);
            self.data.extend_from_slice(&value.to_be_bytes());
        }
    }

    fn header(&mut self, length: usize, fix: u8, fix_max: usize, codes: [u8; 3]) {
        if length <= fix_max && fix != 0 {
            self.data.push(fix | length as u8);
        } else if length <= 0xff && codes[0] != 0 {
            self.data.extend_from_slice(&[codes[0], length as u8]);
        } else if length <= 0xffff {
            self.data.push(codes[1]);
            self.data.extend_from_slice(&(length as u16).to_be_bytes());
        } else {
            self.data.push(codes[2]);
            self.data.extend_from_slice(&(length as u32).to_be_bytes());
        }
    }

    pub fn str(&mut self, value: &str) {
        self.header(value.len(), 0xa0, 31, [0xd9, 0xda, 0xdb]);
        self.data.extend_from_slice(value.as_bytes());
    }

    pub fn bin(&mut self, value: &[u8]) {
        self.header(value.len(), 0, 0, [0xc4, 0xc5, 0xc6]);
        self.data.extend_from_slice(value);
    }

    pub fn array(&mut self, length: usize) {
        self.header(length, 0x90, 15, [0, 0xdc, 0xdd]);
    }

    pub fn map(&mut self, length: usize) {
        self.header(length, 0x80, 15, [0, 0xde, 0xdf]);
    }
}
