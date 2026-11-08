//! Go `sort.Slice` / `slices.SortFunc` (pattern-defeating quicksort, not
//! stable). Results Go sorts with them keep Go's exact order for equal keys
//! only when the same algorithm runs; port of Go `sort/zsortfunc.go`.
//!
//! Shared by every crate that reproduces a Go `sort.Slice` order.

struct Data<'a, T, F> {
    items: &'a mut [T],
    less: F,
}

impl<T, F: Fn(&T, &T) -> bool> Data<'_, T, F> {
    fn less(&self, i: usize, j: usize) -> bool {
        (self.less)(&self.items[i], &self.items[j])
    }

    fn swap(&mut self, i: usize, j: usize) {
        self.items.swap(i, j);
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Hint {
    Unknown,
    Increasing,
    Decreasing,
}

/// Go `sort.Slice(items, less)`.
pub fn sort_slice<T>(items: &mut [T], less: impl Fn(&T, &T) -> bool) {
    let length = items.len();
    let limit = (usize::BITS - length.leading_zeros()) as usize;
    let mut data = Data { items, less };
    pdqsort(&mut data, 0, length, limit);
}

fn insertion_sort<T, F: Fn(&T, &T) -> bool>(data: &mut Data<T, F>, a: usize, b: usize) {
    for i in a + 1..b {
        let mut j = i;
        while j > a && data.less(j, j - 1) {
            data.swap(j, j - 1);
            j -= 1;
        }
    }
}

fn sift_down<T, F: Fn(&T, &T) -> bool>(data: &mut Data<T, F>, lo: usize, hi: usize, first: usize) {
    let mut root = lo;
    loop {
        let mut child = 2 * root + 1;
        if child >= hi {
            break;
        }
        if child + 1 < hi && data.less(first + child, first + child + 1) {
            child += 1;
        }
        if !data.less(first + root, first + child) {
            return;
        }
        data.swap(first + root, first + child);
        root = child;
    }
}

fn heap_sort<T, F: Fn(&T, &T) -> bool>(data: &mut Data<T, F>, a: usize, b: usize) {
    let first = a;
    let hi = b - a;
    let mut i = (hi as isize - 1) / 2;
    while i >= 0 {
        sift_down(data, i as usize, hi, first);
        i -= 1;
    }
    let mut i = hi as isize - 1;
    while i >= 0 {
        data.swap(first, first + i as usize);
        sift_down(data, 0, i as usize, first);
        i -= 1;
    }
}

fn pdqsort<T, F: Fn(&T, &T) -> bool>(
    data: &mut Data<T, F>,
    mut a: usize,
    mut b: usize,
    mut limit: usize,
) {
    const MAX_INSERTION: usize = 12;
    let mut was_balanced = true;
    let mut was_partitioned = true;
    loop {
        let length = b - a;
        if length <= MAX_INSERTION {
            insertion_sort(data, a, b);
            return;
        }
        if limit == 0 {
            heap_sort(data, a, b);
            return;
        }
        if !was_balanced {
            break_patterns(data, a, b);
            limit -= 1;
        }
        let (mut pivot, mut hint) = choose_pivot(data, a, b);
        if hint == Hint::Decreasing {
            reverse_range(data, a, b);
            pivot = (b - 1) - (pivot - a);
            hint = Hint::Increasing;
        }
        if was_balanced
            && was_partitioned
            && hint == Hint::Increasing
            && partial_insertion_sort(data, a, b)
        {
            return;
        }
        if a > 0 && !data.less(a - 1, pivot) {
            a = partition_equal(data, a, b, pivot);
            continue;
        }
        let (mid, already_partitioned) = partition(data, a, b, pivot);
        was_partitioned = already_partitioned;
        let (left_len, right_len) = (mid - a, b - mid);
        let balance_threshold = length / 8;
        if left_len < right_len {
            was_balanced = left_len >= balance_threshold;
            pdqsort(data, a, mid, limit);
            a = mid + 1;
        } else {
            was_balanced = right_len >= balance_threshold;
            pdqsort(data, mid + 1, b, limit);
            b = mid;
        }
    }
}

fn partition<T, F: Fn(&T, &T) -> bool>(
    data: &mut Data<T, F>,
    a: usize,
    b: usize,
    pivot: usize,
) -> (usize, bool) {
    data.swap(a, pivot);
    let (mut i, mut j) = (a as isize + 1, b as isize - 1);
    while i <= j && data.less(i as usize, a) {
        i += 1;
    }
    while i <= j && !data.less(j as usize, a) {
        j -= 1;
    }
    if i > j {
        data.swap(j as usize, a);
        return (j as usize, true);
    }
    data.swap(i as usize, j as usize);
    i += 1;
    j -= 1;
    loop {
        while i <= j && data.less(i as usize, a) {
            i += 1;
        }
        while i <= j && !data.less(j as usize, a) {
            j -= 1;
        }
        if i > j {
            break;
        }
        data.swap(i as usize, j as usize);
        i += 1;
        j -= 1;
    }
    data.swap(j as usize, a);
    (j as usize, false)
}

fn partition_equal<T, F: Fn(&T, &T) -> bool>(
    data: &mut Data<T, F>,
    a: usize,
    b: usize,
    pivot: usize,
) -> usize {
    data.swap(a, pivot);
    let (mut i, mut j) = (a as isize + 1, b as isize - 1);
    loop {
        while i <= j && !data.less(a, i as usize) {
            i += 1;
        }
        while i <= j && data.less(a, j as usize) {
            j -= 1;
        }
        if i > j {
            break;
        }
        data.swap(i as usize, j as usize);
        i += 1;
        j -= 1;
    }
    i as usize
}

fn partial_insertion_sort<T, F: Fn(&T, &T) -> bool>(
    data: &mut Data<T, F>,
    a: usize,
    b: usize,
) -> bool {
    const MAX_STEPS: usize = 5;
    const SHORTEST_SHIFTING: usize = 50;
    let mut i = a + 1;
    for _ in 0..MAX_STEPS {
        while i < b && !data.less(i, i - 1) {
            i += 1;
        }
        if i == b {
            return true;
        }
        if b - a < SHORTEST_SHIFTING {
            return false;
        }
        data.swap(i, i - 1);
        if i - a >= 2 {
            let mut j = i - 1;
            while j >= 1 {
                if !data.less(j, j - 1) {
                    break;
                }
                data.swap(j, j - 1);
                j -= 1;
            }
        }
        if b - i >= 2 {
            for j in i + 1..b {
                if !data.less(j, j - 1) {
                    break;
                }
                data.swap(j, j - 1);
            }
        }
    }
    false
}

fn break_patterns<T, F: Fn(&T, &T) -> bool>(data: &mut Data<T, F>, a: usize, b: usize) {
    let length = b - a;
    if length >= 8 {
        let mut random = length as u64;
        let modulus = 1usize << (usize::BITS - length.leading_zeros());
        let start = a + (length / 4) * 2 - 1;
        for idx in start..=start + 2 {
            random ^= random << 13;
            random ^= random >> 7;
            random ^= random << 17;
            let mut other = (random as usize) & (modulus - 1);
            if other >= length {
                other -= length;
            }
            data.swap(idx, a + other);
        }
    }
}

fn choose_pivot<T, F: Fn(&T, &T) -> bool>(
    data: &mut Data<T, F>,
    a: usize,
    b: usize,
) -> (usize, Hint) {
    const SHORTEST_NINTHER: usize = 50;
    const MAX_SWAPS: usize = 4 * 3;
    let l = b - a;
    let mut swaps = 0;
    let mut i = a + l / 4;
    let mut j = a + l / 4 * 2;
    let mut k = a + l / 4 * 3;
    if l >= 8 {
        if l >= SHORTEST_NINTHER {
            i = median(data, i - 1, i, i + 1, &mut swaps);
            j = median(data, j - 1, j, j + 1, &mut swaps);
            k = median(data, k - 1, k, k + 1, &mut swaps);
        }
        j = median(data, i, j, k, &mut swaps);
    }
    match swaps {
        0 => (j, Hint::Increasing),
        MAX_SWAPS => (j, Hint::Decreasing),
        _ => (j, Hint::Unknown),
    }
}

fn order2<T, F: Fn(&T, &T) -> bool>(
    data: &Data<T, F>,
    a: usize,
    b: usize,
    swaps: &mut usize,
) -> (usize, usize) {
    if data.less(b, a) {
        *swaps += 1;
        return (b, a);
    }
    (a, b)
}

fn median<T, F: Fn(&T, &T) -> bool>(
    data: &Data<T, F>,
    a: usize,
    b: usize,
    c: usize,
    swaps: &mut usize,
) -> usize {
    let (a, b) = order2(data, a, b, swaps);
    let (b, c) = order2(data, b, c, swaps);
    let _ = c;
    let (_, b) = order2(data, a, b, swaps);
    b
}

fn reverse_range<T, F: Fn(&T, &T) -> bool>(data: &mut Data<T, F>, a: usize, b: usize) {
    let (mut i, mut j) = (a, b - 1);
    while i < j {
        data.swap(i, j);
        i += 1;
        j -= 1;
    }
}

#[cfg(test)]
mod tests {
    use super::sort_slice;

    #[test]
    fn sorts_like_a_total_order() {
        let mut values: Vec<u32> = (0..500).map(|value| (value * 7919) % 211).collect();
        sort_slice(&mut values, |left, right| left < right);
        assert!(values.windows(2).all(|pair| pair[0] <= pair[1]));
    }

    /// Permutations of `sort.Slice` over equal keys, recorded with Go 1.27.
    #[test]
    fn matches_go_order_for_equal_keys() {
        let expected: [(usize, &str); 3] = [
            (9, "0,7,1,8,2,3,4,5,6"),
            (
                50,
                "0,39,26,13,33,20,46,7,27,1,14,40,21,34,8,47,15,41,2,28,35,48,9,22,16,42,3,29,36,23,10,49,30,17,4,43,11,24,37,18,31,44,5,25,38,12,32,19,45,6",
            ),
            (
                300,
                "273,52,221,195,299,156,234,130,117,169,91,39,182,13,247,65,143,78,286,104,208,0,260,26,72,280,241,20,176,111,150,163,215,33,59,137,189,202,267,293,85,254,7,228,98,124,46,144,209,92,261,1,157,53,196,118,131,40,170,14,79,287,248,274,235,105,66,183,27,222,281,151,242,73,138,21,177,112,203,60,34,125,268,216,229,8,86,99,294,190,164,255,47,80,249,93,223,2,158,171,288,15,145,28,236,275,106,184,210,132,41,67,262,54,197,119,191,230,217,61,165,295,204,113,256,48,126,243,9,87,152,100,282,22,269,178,139,35,74,172,237,211,185,276,133,81,68,250,16,29,107,146,42,263,289,94,198,3,224,159,120,55,75,192,88,244,166,218,101,296,62,153,10,257,205,114,49,231,36,179,127,283,270,140,23,121,173,277,30,134,82,4,238,225,69,95,160,186,147,199,264,43,17,251,212,290,56,108,37,219,258,206,11,115,154,102,297,63,167,50,245,284,180,141,89,193,24,76,128,271,232,122,109,161,5,291,174,148,226,239,18,70,83,135,96,278,187,31,265,200,57,213,252,44,142,12,103,233,207,259,116,298,168,155,77,51,285,38,90,181,194,129,272,64,220,246,25,71,266,279,32,136,19,123,175,214,240,97,188,162,227,58,292,149,6,84,201,253,45,110",
            ),
        ];
        for (length, order) in expected {
            let mut items: Vec<(usize, usize)> = (0..length)
                .map(|index| ((index * 7919) % 13, index))
                .collect();
            sort_slice(&mut items, |left, right| left.0 < right.0);
            let ids: Vec<String> = items.iter().map(|item| item.1.to_string()).collect();
            assert_eq!(ids.join(","), order, "length {length}");
        }
    }
}
