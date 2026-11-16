#!/usr/bin/env python3

import itertools
import random
import statistics
import time

NODE_SIZE = 1024 # units of minimum write
MAX_PIVOT = 256

class ZipfGenerator:
    def __init__(self):
        self.population = range(1, 2048 + 1)
        weights = (1 / k for k in self.population)
        self.cum_weights = list(itertools.accumulate(weights))

    def sample(self, n: int) -> int:
        return random.choices(
            self.population[:n],
            cum_weights=self.cum_weights[:n],
            k=1)[0]

rando = ZipfGenerator()

def Partition(data:int, kids: int):
    # we pretend the log if made of of chunks that are Zipf
    # distributed in size up to the size of the log and randomly
    # assign them to children.
    parts = [0] * kids
    while data > 0:
        part = rando.sample(data)
        data -= part
        parts[random.randrange(kids)] += part
    return parts

class Stats:
    def __init__(self, indata, outdata=0, writes=0, blocks=0):
        self.indata = indata
        self.outdata = outdata
        self.writes = writes
        self.blocks = blocks

    def add(self, other):
        return Stats(
            self.indata,
            self.outdata + other.outdata,
            self.writes + other.writes,
            self.blocks + other.blocks)

    def record_append(self, data):
        return Stats(
            self.indata,
            self.outdata + data,
            self.writes + 1,
            self.blocks)

    def record_update(self, data, count):
        return Stats(
            self.indata,
            self.outdata + data,
            self.writes + count,
            self.blocks + count)

class StatsCollector:
    def __init__(self):
        self.amplification = []
        self.bytes_written = []
        self.writes = []
        self.blocks = []

    def update(self, stats):
        amplification = stats.outdata / stats.indata
        self.amplification.append(amplification)
        self.bytes_written.append(stats.outdata * 4096)
        self.writes.append(stats.writes)
        self.blocks.append(stats.blocks)

    def print_deciles(self):
        a = statistics.quantiles(self.amplification, n=100)
        bw = statistics.quantiles(self.bytes_written, n=100)
        w = statistics.quantiles(self.writes, n=100)
        b = statistics.quantiles(self.blocks, n=100)
        for i in range(9,89,10):
            print(f"{i+1:4}%: {a[i]:20,.2f} {bw[i]:20,.2f} {w[i]:20,.2f} {b[i]:20,.2f}")
        for i in range(90,99,1):
            print(f"{i+1:4}%: {a[i]:20,.2f} {bw[i]:20,.2f} {w[i]:20,.2f} {b[i]:20,.2f}")

        ma = max(self.amplification)
        mbw = max(self.bytes_written)
        mw = max(self.writes)
        mb = max(self.blocks)
        print(f"  max: {ma:20,.2f} {mbw:20,.2f} {mw:20,.2f} {mb:20,.2f}")
        ma = statistics.mean(self.amplification)
        mbw = statistics.mean(self.bytes_written)
        mw = statistics.mean(self.writes)
        mb = statistics.mean(self.blocks)
        print(f" mean: {ma:20,.2f} {mbw:20,.2f} {mw:20,.2f} {mb:20,.2f}")
        
    
class Leaf:
    def __init__(self, data: int):
        self.data = data

    def Upsert(self, data: int, stats: Stats):
        newdata = self.data + data
        if newdata > NODE_SIZE:
            left = newdata // 2
            right = newdata - left
            return [Leaf(left), Leaf(right)], stats.record_update(newdata, 2)
        return [Leaf(newdata)], stats.record_append(data)

class Branch:
    def __init__(self, pivot, data: int):
        self.pivot = pivot
        self.data = data

    def Upsert(self, data: int, stats: Stats):
        if self.data + data > NODE_SIZE:
            return self.Rewrite(data, stats)
        return [Branch(self.pivot, self.data + data)], stats.record_append(data)
    
    def Rewrite(self, data: int, stats: Stats):
        newkids = []
        for i, l in enumerate(Partition(self.data + data, len(self.pivot))):
            if l > 0:
                kids, stats = self.pivot[i].Upsert(l, stats)
                newkids.extend(kids)
            else:
                newkids.append(self.pivot[i])
        if len(newkids) > MAX_PIVOT:
            mid = len(newkids) // 2
            return ([Branch(newkids[:mid], 0), Branch(newkids[mid:], 0)],
                    stats.record_update(0, 2))
        return [Branch(newkids, 0)], stats.record_update(0, 1)


class BTree:
    def __init__(self):
        self.root = Leaf(0)

    def Upsert(self, data: int):
        stats = Stats(data)
        newroots, stats = self.root.Upsert(data, stats)
        if len(newroots) > 1:
            self.root = Branch(newroots, 0)
            stats = stats.record_update(0, 1)
        elif len(newroots) == 1:
            self.root = newroots[0]
        else:
            # won't happen until we add deletes
            self.root = Leaf(0)
        return stats

def run(m, n: int):
    start = time.monotonic()
    collector = StatsCollector()
    for i in range(m):
        bt = BTree()
        for _ in range(n):
            data = rando.sample(256) # largest write will be 1 MiB
            stats = bt.Upsert(data)
            collector.update(stats)
        if time.monotonic() - start > 1:
            print(f"working... {100*i/m:.1f}%")
            start = time.monotonic()
    #       sums:            363,081.45    62,887,297,024.00           213,291.00            18,689.00
    print(f"              amplification                bytes               writes               blocks")
    collector.print_deciles()


run(50, 100_000)


